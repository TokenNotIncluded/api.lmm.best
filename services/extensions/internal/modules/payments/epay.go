package payments

import (
	"context"
	"crypto/md5" // Required by the legacy ePay protocol, not used for new protocols.
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type EPayConfig struct {
	Channel            Channel // The legacy wire format has no currency: CNY only.
	Endpoint           string
	MerchantKey        string
	PaymentType        string
	NotifyURL          string
	ReturnURL          string
	EnableLegacyRefund bool // Default false: no standard refund identity/proof API.
	AllowPartialRefund bool // Enable only after testing this specific provider.
}
type EPay struct {
	cfg    EPayConfig
	client *http.Client
}

func NewEPay(cfg EPayConfig) (*EPay, error) {
	if !cfg.Channel.valid() || cfg.Channel.Provider != "epay" || cfg.Channel.Currency != "CNY" || len(cfg.MerchantKey) < 16 || !validID(cfg.PaymentType) {
		return nil, ErrInvalid
	}
	for _, c := range cfg.Channel.Merchant {
		if c < '0' || c > '9' {
			return nil, ErrInvalid
		}
	}
	u, err := secureURL(cfg.Endpoint)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrInvalid
	}
	if _, err = secureURL(cfg.NotifyURL); err != nil {
		return nil, err
	}
	if _, err = secureURL(cfg.ReturnURL); err != nil {
		return nil, err
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/") + "/"
	return &EPay{cfg: cfg, client: outboundClient()}, nil
}
func (e *EPay) Channel() Channel     { return e.cfg.Channel }
func (e *EPay) RefundEnabled() bool  { return e.cfg.EnableLegacyRefund }
func (e *EPay) PartialRefunds() bool { return e.cfg.AllowPartialRefund }
func (*EPay) ReplayWindow(operation string) time.Duration {
	if operation == "checkout" {
		return 100 * 365 * 24 * time.Hour
	}
	return 0
}
func epaySign(v url.Values, key string) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		if k != "sign" && k != "sign_type" && v.Get(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+v.Get(k))
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + key))
	return hex.EncodeToString(sum[:])
}
func minorDecimal(s string) (int64, error) {
	if s == "" || len(s) > 18 {
		return 0, ErrInvalid
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, ErrInvalid
	}
	for _, part := range parts {
		if part == "" {
			return 0, ErrInvalid
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, ErrInvalid
			}
		}
	}
	if len(parts) == 2 && len(parts[1]) > 2 {
		return 0, ErrInvalid
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > maxAmount/100 {
		return 0, ErrInvalid
	}
	var fraction int64
	if len(parts) == 2 {
		f := parts[1]
		if len(f) == 1 {
			f += "0"
		}
		fraction, _ = strconv.ParseInt(f, 10, 64)
	}
	amount := whole*100 + fraction
	if !validAmount(amount) {
		return 0, ErrInvalid
	}
	return amount, nil
}
func decimalMinor(n int64) string {
	return strconv.FormatInt(n/100, 10) + "." + string([]byte{'0' + byte(n%100/10), '0' + byte(n%10)})
}
func (e *EPay) Checkout(_ context.Context, o Order, _ string) (Checkout, error) {
	if o.Intent.Currency != "CNY" || !validAmount(o.Intent.AmountMinor) {
		return Checkout{}, ErrInvalid
	}
	v := url.Values{"pid": {e.cfg.Channel.Merchant}, "type": {e.cfg.PaymentType}, "out_trade_no": {o.ID}, "notify_url": {e.cfg.NotifyURL}, "return_url": {e.cfg.ReturnURL}, "name": {"Account top-up"}, "money": {decimalMinor(o.Intent.AmountMinor)}, "sign_type": {"MD5"}}
	v.Set("sign", epaySign(v, e.cfg.MerchantKey))
	return Checkout{ID: o.ID, URL: e.cfg.Endpoint + "submit.php?" + v.Encode()}, nil
}
func (e *EPay) Verify(r *http.Request, _ time.Time) (Event, error) {
	var raw string
	switch r.Method {
	case "GET":
		raw = r.URL.RawQuery
	case "POST":
		if r.URL.RawQuery != "" {
			return Event{}, ErrInvalid
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/x-www-form-urlencoded" {
			return Event{}, ErrInvalid
		}
		b, err := boundedBytes(r.Body)
		if err != nil {
			return Event{}, err
		}
		raw = string(b)
	default:
		return Event{}, ErrInvalid
	}
	if len(raw) > MaxEvidence {
		return Event{}, ErrInvalid
	}
	v, err := url.ParseQuery(raw)
	if err != nil || len(v) > 32 {
		return Event{}, ErrInvalid
	}
	for k, values := range v {
		if !validID(k) || len(values) != 1 || strings.ContainsAny(values[0], "&\r\n") {
			return Event{}, ErrInvalid
		}
	}
	if v.Get("sign_type") != "MD5" || v.Get("pid") != e.cfg.Channel.Merchant || v.Get("type") != e.cfg.PaymentType {
		return Event{}, ErrDenied
	}
	received, err := hex.DecodeString(v.Get("sign"))
	if err != nil || len(received) != md5.Size {
		return Event{}, ErrDenied
	}
	expected, _ := hex.DecodeString(epaySign(v, e.cfg.MerchantKey))
	if subtle.ConstantTimeCompare(received, expected) != 1 {
		return Event{}, ErrDenied
	}
	if v.Get("trade_status") != "TRADE_SUCCESS" {
		return Event{}, ErrUnsupported
	}
	amount, err := minorDecimal(v.Get("money"))
	if err != nil {
		return Event{}, err
	}
	if !validID(v.Get("out_trade_no")) || !validID(v.Get("trade_no")) {
		return Event{}, ErrInvalid
	}
	c := e.cfg.Channel
	return Event{OrderID: v.Get("out_trade_no"), Type: Paid, Currency: "CNY", AmountMinor: amount,
		Evidence: Evidence{Provider: c.Provider, Merchant: c.Merchant, Environment: c.Environment, Transaction: v.Get("trade_no"), EventID: stableKey(v.Get("trade_no"), "TRADE_SUCCESS"), Payload: []byte(raw), Signature: []byte(v.Get("sign")), Source: "webhook"}}, nil
}
func (e *EPay) Lookup(ctx context.Context, o Order) ([]Event, error) {
	q := url.Values{"act": {"order"}, "pid": {e.cfg.Channel.Merchant}, "key": {e.cfg.MerchantKey}, "out_trade_no": {o.ID}}
	// This legacy API places the key in the URL. Never log request URLs, and never
	// follow redirects. Providers with a POST query profile need their own adapter.
	raw, err := requestJSON(ctx, e.client, "GET", e.cfg.Endpoint+"api.php?"+q.Encode(), nil, http.Header{})
	if err != nil {
		return nil, err
	}
	var v struct {
		Code   int             `json:"code"`
		PID    json.RawMessage `json:"pid"`
		Trade  string          `json:"trade_no"`
		Order  string          `json:"out_trade_no"`
		Money  json.RawMessage `json:"money"`
		Status *int            `json:"status"`
		Type   string          `json:"type"`
	}
	if jsonObject(raw, &v) != nil || v.Code != 1 {
		return nil, ErrUnavailable
	}
	amount, err := minorDecimal(strings.Trim(string(v.Money), "\""))
	if err != nil || strings.Trim(string(v.PID), "\"") != e.cfg.Channel.Merchant || v.Order != o.ID || amount != o.Intent.AmountMinor || v.Type != e.cfg.PaymentType || v.Status == nil {
		return nil, ErrConflict
	}
	if *v.Status != 1 {
		return nil, nil
	}
	c := e.cfg.Channel
	event := Event{OrderID: o.ID, Type: Paid, Currency: "CNY", AmountMinor: amount, Evidence: Evidence{Provider: c.Provider, Merchant: c.Merchant, Environment: c.Environment, Transaction: v.Trade, Payload: raw}}
	apiEvidence(&event, v.Trade, "paid")
	return []Event{event}, nil
}
func (e *EPay) Refund(ctx context.Context, o Order, r Refund, _ string) (RefundResult, error) {
	if !e.cfg.EnableLegacyRefund || r.AmountMinor != o.Intent.AmountMinor && !e.cfg.AllowPartialRefund {
		return RefundResult{}, ErrUnsupported
	}
	if r.HoldReceipt == "" || !validAmount(r.AmountMinor) || o.PaymentID == "" {
		return RefundResult{}, ErrInvalid
	}
	f := url.Values{"pid": {e.cfg.Channel.Merchant}, "key": {e.cfg.MerchantKey}, "trade_no": {o.PaymentID}, "money": {decimalMinor(r.AmountMinor)}}
	raw, err := requestJSON(ctx, e.client, "POST", e.cfg.Endpoint+"api.php?act=refund", f, http.Header{})
	if err != nil {
		return RefundResult{}, err
	}
	var v struct {
		Code int `json:"code"`
	}
	if jsonObject(raw, &v) != nil {
		return RefundResult{}, ErrInvalid
	}
	status := "reported_failed"
	if v.Code == 1 {
		status = "reported_succeeded"
	}
	// No invented refund ID or signature. A verified terminal core receipt is
	// still required. This call must never be retried after an unknown outcome.
	return RefundResult{Status: status}, nil
}
