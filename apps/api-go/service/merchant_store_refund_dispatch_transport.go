package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// Customer refund calls need the freshly minted, order-bound Bearer token.
// The checkout transport intentionally strips Authorization, so this separate
// transport permits exactly one documented customer action and no redirects.
type merchantStoreRefundCustomerTransport struct {
	base        http.RoundTripper
	token       string
	environment string
}

func (t merchantStoreRefundCustomerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r == nil || r.URL == nil || r.Method != http.MethodPost || r.URL.String() != pancake.DefaultBaseURL+"/v1/actions/refund-ticket/create-ticket" || r.Header.Get("Authorization") != "Bearer "+t.token || r.Header.Get("X-Environment") != t.environment || t.token == "" {
		return nil, ErrMerchantStoreRefundProvider
	}
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.Header.Del("Cookie")
	c.Header.Del("Proxy-Authorization")
	c.Header.Del("X-Idempotency-Key")
	resp, e := t.base.RoundTrip(c)
	if e != nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	if common.LimitResponseBody(resp, 256<<10) != nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	return resp, nil
}

func merchantStoreRefundRawTransport(orderID string) http.RoundTripper {
	client := newMerchantStorePaymentHTTPClient(orderID)
	return client.Transport.(merchantStoreProviderTransport).base
}

func merchantStoreRefundCustomer(ctx context.Context, d *model.MerchantStoreRefundDispatch) (*pancake.CustomerSession, error) {
	frozen, e := loadMerchantStorePaymentContext(&d.Order)
	if e != nil {
		return nil, e
	}
	// A new query lease safely mints a fresh session; never reuse an expired
	// cached auth response keyed only by the original paid order.
	merchant, e := newMerchantStorePancakeClient(frozen.Config, d.Refund.ID+":"+d.Attempt.LeaseToken)
	if e != nil {
		return nil, e
	}
	tok, e := merchant.Auth.IssueSessionToken(ctx, pancake.IssueSessionTokenParams{StoreID: &frozen.Config.StoreID, BuyerIdentity: model.MerchantStoreOrderBuyerIdentity(&d.Order)})
	if e != nil || tok == nil || tok.Token == "" || len(tok.Token) > 8192 || strings.ContainsAny(tok.Token, "\r\n\x00") {
		return nil, ErrMerchantStoreRefundProvider
	}
	expiry, e := time.Parse(time.RFC3339, tok.ExpiresAt)
	if e != nil || time.Until(expiry) < 30*time.Second {
		return nil, ErrMerchantStoreRefundProvider
	}
	httpClient := &http.Client{Transport: merchantStoreRefundCustomerTransport{base: merchantStoreRefundRawTransport(d.Order.ID), token: tok.Token, environment: frozen.Config.Environment}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrMerchantStoreRefundProvider }}
	client, e := pancake.New(pancake.Config{MerchantID: frozen.Config.MerchantID, PrivateKey: frozen.Config.PrivateKey, Environment: pancake.Environment(frozen.Config.Environment), BaseURL: pancake.DefaultBaseURL, HTTPClient: httpClient})
	if e != nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	return client.Customer(tok.Token), nil
}

type merchantStoreLinuxDORefundTransport struct{ base http.RoundTripper }

func (t merchantStoreLinuxDORefundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r == nil || r.URL == nil || r.Method != http.MethodPost || r.URL.String() != "https://credit.linux.do/epay/api.php" {
		return nil, ErrMerchantStoreRefundProvider
	}
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.Header.Del("Cookie")
	c.Header.Del("Authorization")
	c.Header.Del("Proxy-Authorization")
	c.Header.Del("X-Idempotency-Key")
	resp, e := t.base.RoundTrip(c)
	if e != nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	if common.LimitResponseBody(resp, 64<<10) != nil {
		return nil, ErrMerchantStoreRefundProvider
	}
	return resp, nil
}

// Linux DO documents only a full refund and a synchronous authenticated ACK.
// It exposes neither a refund identifier nor an authoritative refund query.
// The ACK's semantic reference is explicitly our receipt hash, not a fake PSP
// refund ID. Any ambiguous response remains unknown and is never auto-reposted.
func merchantStoreSubmitLinuxDORefund(ctx context.Context, o *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest) (MerchantStoreRefundProviderResult, error) {
	client := &http.Client{Transport: merchantStoreLinuxDORefundTransport{base: merchantStoreRefundRawTransport(o.ID)}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrMerchantStoreRefundProvider }}
	return merchantStoreSubmitLinuxDORefundWithClient(ctx, o, request, client)
}
func merchantStoreSubmitLinuxDORefundWithClient(ctx context.Context, o *model.MerchantStoreOrder, request MerchantStoreRefundNativeRequest, client *http.Client) (MerchantStoreRefundProviderResult, error) {
	result := MerchantStoreRefundProviderResult{State: MerchantStoreRefundProviderUnknown, Code: "manual_verification_required"}
	frozen, e := merchantStoreRefundValidateRequest(o, request)
	if e != nil || o.PaymentMethod != MerchantStorePlatformLinuxDO || request.AmountMinor != request.Payment.AmountMinor || request.Payment.PaymentReference != o.ProviderTradeID || request.Currency != "LDC" || strings.TrimRight(frozen.Config.GatewayURL, "/") != "https://credit.linux.do/epay" {
		return result, ErrMerchantStoreRefundProvider
	}
	body, e := json.Marshal(map[string]string{"pid": frozen.Config.PartnerID, "key": frozen.Config.Key, "trade_no": o.ProviderTradeID, "out_trade_no": o.TradeNo, "money": merchantStoreMinorMoney(request.AmountMinor)})
	if e != nil {
		return result, ErrMerchantStoreRefundProvider
	}
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://credit.linux.do/epay/api.php", bytes.NewReader(body))
	if e != nil {
		return result, ErrMerchantStoreRefundProvider
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(r)
	if e != nil {
		return result, ErrMerchantStoreRefundProvider
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, ErrMerchantStoreRefundProvider
	}
	payload, e := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if e != nil || len(payload) > 64<<10 {
		return result, ErrMerchantStoreRefundProvider
	}
	var ack struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(payload, &ack) != nil || ack.Code != 1 || ack.Msg != "退款成功" {
		return result, ErrMerchantStoreRefundProvider
	}
	facts, _ := json.Marshal([]any{request.Payment.EvidenceHash, request.Payment.PaymentReference, o.TradeNo, request.RefundID, request.AmountMinor, request.Currency, "authenticated-full-refund-ack"})
	hash := merchantStoreRefundEvidenceHash("linuxdo-full-refund-ack-v1", o, request.RefundID, facts)
	result.State, result.Code = MerchantStoreRefundProviderSucceeded, "provider_refund_completed"
	result.evidence = &MerchantStoreRefundVerifiedNativeEvidence{PaymentReference: request.Payment.PaymentReference, RefundReference: "ldc-ack-" + hash, AmountMinor: request.AmountMinor, Currency: request.Currency, EvidenceHash: hash}
	return result, nil
}
