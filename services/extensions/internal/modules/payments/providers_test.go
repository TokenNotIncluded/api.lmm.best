package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(s string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(s))}
}
func stripeFixture(t *testing.T) *Stripe {
	t.Helper()
	s, err := NewStripe(StripeConfig{Channel: Channel{ID: 1, Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Currency: "USD", Enabled: true}, APIKey: "sk_test_not_a_real_secret", APIVersion: "fixture-api-version", WebhookSecrets: []string{"whsec_fixture_current_secret", "whsec_fixture_old_secret"}, SuccessURL: "https://example.com/paid", CancelURL: "https://example.com/canceled"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const paymentObject = `{"id":"pi_top_1","object":"payment_intent","status":"succeeded","amount":1000,"amount_received":1000,"currency":"usd","livemode":false,"metadata":{"lmm_order_id":"top_1"}}`

func envelope(kind, id, object string) string {
	return `{"id":"` + id + `","type":"` + kind + `","livemode":false,"created":1,"data":{"object":` + object + `}}`
}
func signatureFor(raw, secret string, now time.Time) string {
	stamp := strconv.FormatInt(now.Unix(), 10)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stamp + "." + raw))
	return "t=" + stamp + ",v1=" + hex.EncodeToString(h.Sum(nil))
}
func stripeRequest(raw, secret string, now time.Time) *http.Request {
	r := httptest.NewRequest("POST", "https://example.com/callbacks/1", strings.NewReader(raw))
	r.Header.Set("Stripe-Signature", signatureFor(raw, secret, now))
	return r
}
func TestStripeRawSignatureAndMerchantProof(t *testing.T) {
	s := stripeFixture(t)
	now := time.Unix(1791633600, 0)
	raw := envelope("payment_intent.succeeded", "evt_fixture", paymentObject)
	calls := 0
	s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer sk_test_not_a_real_secret" || r.Header.Get("Stripe-Version") != "fixture-api-version" {
			t.Error("missing configured merchant authentication")
		}
		switch r.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/events/evt_fixture":
			return response(raw), nil
		default:
			t.Fatal(r.URL.Path)
			return nil, ErrInvalid
		}
	})}
	e, err := s.Verify(stripeRequest(raw, s.cfg.WebhookSecrets[0], now), now)
	if err != nil || e.AmountMinor != 1000 || e.OrderID != "top_1" || e.Evidence.Source != "webhook" || string(e.Evidence.Payload) != raw || calls != 2 {
		t.Fatal(e, err, calls)
	} // Event creation time is old, but this delivery was freshly signed.
	tampered := stripeRequest(raw, s.cfg.WebhookSecrets[0], now)
	tampered.Body = io.NopCloser(strings.NewReader(strings.Replace(raw, "1000", "1001", 1)))
	if _, err = s.Verify(tampered, now); !errors.Is(err, ErrDenied) || calls != 2 {
		t.Fatal("tampered body reached provider", err, calls)
	}
}
func TestStripeSignatureRotationFreshnessAndHeaderAmbiguity(t *testing.T) {
	now := time.Unix(1791633600, 0)
	secret := "whsec_fixture_old_secret"
	payload := []byte(`{"x":1}`)
	good := signatureFor(string(payload), secret, now)
	for _, tc := range []struct {
		name, header string
		when         time.Time
		ok           bool
	}{{"valid", good, now, true}, {"old_delivery", good, now.Add(301 * time.Second), false}, {"future_delivery", good, now.Add(-301 * time.Second), false}, {"duplicate_timestamp", good + ",t=1", now, false}, {"multiple_signatures", good + ",v1=" + strings.Repeat("0", 64), now, true}, {"bad_hex", "t=1791633600,v1=z", now, false}, {"missing_signature", "t=1791633600", now, false}} {
		t.Run(tc.name, func(t *testing.T) {
			got := stripeSignature(payload, tc.header, []string{"whsec_fixture_new_secret", secret}, tc.when)
			if got != tc.ok {
				t.Fatal(got)
			}
		})
	}
}
func TestStripeRejectsMerchantEnvironmentAndDuplicateJSON(t *testing.T) {
	for _, mode := range []string{"merchant", "environment", "duplicate", "wrong_event", "connect"} {
		t.Run(mode, func(t *testing.T) {
			s := stripeFixture(t)
			now := time.Unix(1791633600, 0)
			raw := envelope("payment_intent.succeeded", "evt_fixture", paymentObject)
			if mode == "environment" {
				raw = strings.Replace(raw, `"livemode":false`, `"livemode":true`, 1)
			}
			if mode == "duplicate" {
				raw = strings.Replace(raw, `"amount":1000`, `"amount":1000,"amount":1`, 1)
			}
			if mode == "connect" {
				raw = strings.Replace(raw, `"created":1`, `"created":1,"account":"acct_other"`, 1)
			}
			s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v1/account" {
					if mode == "merchant" {
						return response(`{"id":"acct_wrong"}`), nil
					}
					return response(`{"id":"acct_fixture"}`), nil
				}
				if mode == "wrong_event" {
					return response(strings.ReplaceAll(raw, "evt_fixture", "evt_other")), nil
				}
				return response(raw), nil
			})}
			if _, err := s.Verify(stripeRequest(raw, s.cfg.WebhookSecrets[0], now), now); err == nil {
				t.Fatal("bad provider binding accepted")
			}
		})
	}
}
func TestStripeExternalRefundResolvesOriginalPayment(t *testing.T) {
	s := stripeFixture(t)
	now := time.Unix(1791633600, 0)
	raw := envelope("refund.updated", "evt_refund", `{"id":"re_external","object":"refund","payment_intent":"pi_top_1","amount":300,"currency":"usd","status":"succeeded","metadata":{}}`)
	s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/events/evt_refund":
			return response(raw), nil
		case "/v1/payment_intents/pi_top_1":
			return response(paymentObject), nil
		default:
			t.Fatal(r.URL.Path)
			return nil, ErrInvalid
		}
	})}
	e, err := s.Verify(stripeRequest(raw, s.cfg.WebhookSecrets[0], now), now)
	if err != nil || e.OrderID != "top_1" || e.RefundID != "re_external" || e.Type != RefundSucceeded || e.LocalRefundID != "" {
		t.Fatal(e, err)
	}
}
func TestStripeCheckoutAndRefundUseStableKeysAndIntegerAmounts(t *testing.T) {
	s := stripeFixture(t)
	o := Order{ID: "top_1", PaymentID: "pi_top_1", Intent: Intent{Currency: "USD", AmountMinor: 1000}}
	r := Refund{ID: "local_refund", HoldReceipt: "held_by_core", AmountMinor: 300}
	writes := 0
	s.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/account" {
			return response(`{"id":"acct_fixture"}`), nil
		}
		writes++
		if req.Method != "POST" || req.Header.Get("Idempotency-Key") != "stable_request_01" {
			t.Error("missing idempotency")
		}
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch req.URL.Path {
		case "/v1/checkout/sessions":
			if req.Form.Get("line_items[0][price_data][unit_amount]") != "1000" || req.Form.Get("payment_intent_data[metadata][lmm_order_id]") != "top_1" || req.Form.Get("adaptive_pricing[enabled]") != "false" {
				t.Error(req.Form)
			}
			return response(`{"id":"cs_fixture","object":"checkout.session","client_reference_id":"top_1","metadata":{"lmm_order_id":"top_1"},"amount_total":1000,"currency":"usd","livemode":false,"url":"https://checkout.stripe.com/c/pay/test"}`), nil
		case "/v1/refunds":
			if req.Form.Get("amount") != "300" || req.Form.Get("metadata[lmm_refund_id]") != r.ID {
				t.Error(req.Form)
			}
			return response(`{"id":"re_fixture","object":"refund","payment_intent":"pi_top_1","amount":300,"currency":"usd","status":"succeeded","metadata":{"lmm_order_id":"top_1","lmm_refund_id":"local_refund"}}`), nil
		default:
			t.Fatal(req.URL.Path)
			return nil, ErrInvalid
		}
	})}
	if _, err := s.Checkout(context.Background(), o, "stable_request_01"); err != nil {
		t.Fatal(err)
	}
	result, err := s.Refund(context.Background(), o, r, "stable_request_01")
	if err != nil || result.ID != "re_fixture" || writes != 2 {
		t.Fatal(result, err)
	}
	r.HoldReceipt = ""
	if _, err = s.Refund(context.Background(), o, r, "stable_request_01"); err == nil {
		t.Fatal("refund sent without hold")
	}
}
func TestStripeReconciliationPaginatesAndDoesNotInventSignature(t *testing.T) {
	s := stripeFixture(t)
	pages := 0
	s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/payment_intents/pi_top_1":
			return response(paymentObject), nil
		case "/v1/refunds":
			pages++
			if r.URL.Query().Get("limit") != "100" {
				t.Error("unbounded list")
			}
			if pages == 2 && r.URL.Query().Get("starting_after") != "re_1" {
				t.Error("missing cursor")
			}
			return response(fmt.Sprintf(`{"has_more":%v,"data":[{"id":"re_%d","object":"refund","payment_intent":"pi_top_1","amount":100,"currency":"usd","status":"succeeded","metadata":{}}]}`, pages < 2, pages)), nil
		default:
			return nil, ErrInvalid
		}
	})}
	events, err := s.Lookup(context.Background(), Order{ID: "top_1", PaymentID: "pi_top_1", Intent: Intent{AmountMinor: 1000, Currency: "USD"}})
	if err != nil || len(events) != 3 || pages != 2 {
		t.Fatal(len(events), pages, err)
	}
	for _, e := range events {
		if e.Evidence.Source != "api" || len(e.Evidence.Signature) != 0 {
			t.Fatal("fabricated provider proof")
		}
	}
}
func TestStripeReconciliationPageLimitFailsClosed(t *testing.T) {
	s := stripeFixture(t)
	pages := 0
	s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/payment_intents/pi_top_1":
			return response(paymentObject), nil
		default:
			pages++
			return response(fmt.Sprintf(`{"has_more":true,"data":[{"id":"re_%d","object":"refund","payment_intent":"pi_top_1","amount":1,"currency":"usd","status":"succeeded"}]}`, pages)), nil
		}
	})}
	events, err := s.Lookup(context.Background(), Order{ID: "top_1", PaymentID: "pi_top_1", Intent: Intent{AmountMinor: 1000, Currency: "USD"}})
	if err == nil || len(events) != 0 || pages != 10 {
		t.Fatal("incomplete reconciliation reported complete", pages, err)
	}
}

func epayFixture(t *testing.T) *EPay {
	t.Helper()
	e, err := NewEPay(EPayConfig{Channel: Channel{ID: 2, Provider: "epay", Merchant: "1001", Environment: "test", Currency: "CNY", Enabled: true}, Endpoint: "https://pay.example.com/", MerchantKey: "fixture-merchant-key", PaymentType: "alipay", NotifyURL: "https://example.com/callbacks/2", ReturnURL: "https://example.com/paid"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func epayValues() url.Values {
	return url.Values{"pid": {"1001"}, "type": {"alipay"}, "trade_no": {"t1"}, "out_trade_no": {"top_1"}, "money": {"10.00"}, "name": {"test"}, "trade_status": {"TRADE_SUCCESS"}, "sign_type": {"MD5"}, "sign": {"dfd6dd8d0af5b2cc6724056f3cf4b660"}}
}
func TestEPayReferenceSignatureCallbackAndDeterministicCheckout(t *testing.T) {
	e := epayFixture(t)
	v := epayValues()
	if epaySign(v, e.cfg.MerchantKey) != v.Get("sign") {
		t.Fatal("canonical MD5 test vector mismatch")
	}
	event, err := e.Verify(httptest.NewRequest("GET", "https://example.com/callbacks/2?"+v.Encode(), nil), time.Now())
	if err != nil || event.AmountMinor != 1000 || event.Currency != "CNY" || event.Type != Paid {
		t.Fatal(event, err)
	}
	o := Order{ID: "top_1", Intent: Intent{Currency: "CNY", AmountMinor: 1001}}
	a, err := e.Checkout(context.Background(), o, "key1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Checkout(context.Background(), o, "key2")
	if err != nil || a != b {
		t.Fatal("ePay retry changed order")
	}
	u, _ := url.Parse(a.URL)
	if u.Query().Get("money") != "10.01" || u.Query().Get("sign") != epaySign(u.Query(), e.cfg.MerchantKey) {
		t.Fatal("invalid checkout amount/signature")
	}
	if e.RefundEnabled() || e.PartialRefunds() || e.ReplayWindow("refund_submit") != 0 {
		t.Fatal("unsafe legacy refund default")
	}
}
func TestEPayRejectsTamperDuplicatesAndWrongMerchant(t *testing.T) {
	e := epayFixture(t)
	for _, kind := range []string{"amount", "duplicate", "merchant", "sign_type", "malformed", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			v := epayValues()
			switch kind {
			case "amount":
				v.Set("money", "1000.00")
			case "duplicate":
				v.Add("money", "1000.00")
			case "merchant":
				v.Set("pid", "1002")
				v.Set("sign", epaySign(v, e.cfg.MerchantKey))
			case "sign_type":
				v.Set("sign_type", "SHA256")
			case "malformed":
				v.Set("money", "1e3")
				v.Set("sign", epaySign(v, e.cfg.MerchantKey))
			}
			r := httptest.NewRequest("GET", "https://example.com/callbacks/2?"+v.Encode(), nil)
			if kind == "mixed" {
				r = httptest.NewRequest("POST", "https://example.com/callbacks/2?pid=1001", strings.NewReader(v.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if _, err := e.Verify(r, time.Now()); err == nil {
				t.Fatal("invalid callback accepted")
			}
		})
	}
}
func TestEPayDecimalParsingIsExact(t *testing.T) {
	for _, s := range []string{"0", "0.00", "-1", "+1", "1e2", "1.001", "NaN", "1.", ".1", " 1", "9223372036854775807", "90000000000.01"} {
		if _, err := minorDecimal(s); err == nil {
			t.Fatal("bad amount accepted", s)
		}
	}
	for s, want := range map[string]int64{"0.01": 1, "1": 100, "1.2": 120, "123.45": 12345, "90000000000.00": maxAmount} {
		got, err := minorDecimal(s)
		if err != nil || got != want {
			t.Fatal(s, got, err)
		}
	}
}
func TestEPayLookupAndLegacyRefundKeepUnsignedResultsPending(t *testing.T) {
	e := epayFixture(t)
	e.cfg.EnableLegacyRefund = true
	e.cfg.AllowPartialRefund = true
	writes := 0
	e.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			if r.URL.Query().Get("key") != "fixture-merchant-key" {
				t.Error("missing merchant key")
			}
			return response(`{"code":1,"pid":1001,"trade_no":"t1","out_trade_no":"top_1","money":"10.00","status":1,"type":"alipay"}`), nil
		}
		writes++
		_ = r.ParseForm()
		if r.PostForm.Get("money") != "3.00" || r.PostForm.Get("trade_no") != "t1" {
			t.Error(r.PostForm)
		}
		return response(`{"code":1,"msg":"ok"}`), nil
	})}
	o := Order{ID: "top_1", PaymentID: "t1", Intent: Intent{Currency: "CNY", AmountMinor: 1000}}
	events, err := e.Lookup(context.Background(), o)
	if err != nil || len(events) != 1 || events[0].Evidence.Source != "api" || len(events[0].Evidence.Signature) != 0 {
		t.Fatal(events, err)
	}
	r, err := e.Refund(context.Background(), o, Refund{AmountMinor: 300, HoldReceipt: "core_hold"}, "ignored_legacy_key")
	if err != nil || r.ID != "" || r.Status != "reported_succeeded" || writes != 1 {
		t.Fatal(r, err)
	}
}
func TestPaymentHTTPCallbackAckMeansDurableReceiptOnly(t *testing.T) {
	e := epayFixture(t)
	m := newMemory()
	c := newCore(e.Channel())
	s, err := New(m, c, e)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/callbacks/2?"+epayValues().Encode(), nil)
	w := httptest.NewRecorder()
	s.WebhookHandler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "success" || len(m.events) != 1 || c.count("credit") != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "/orders/top_1", nil)
	w = httptest.NewRecorder()
	s.WebhookHandler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("public webhook listener exposed user API")
	}
}
func TestNetworkPolicyAndBoundedParsing(t *testing.T) {
	for _, s := range []string{"http://example.com", "https://localhost/x", "https://127.0.0.1", "https://[::1]", "https://10.0.0.1", "https://user:secret@example.com", "https://example.com:8080"} {
		if _, err := secureURL(s); err == nil {
			t.Fatal("unsafe URL", s)
		}
	}
	for _, s := range []string{"100.100.100.100", "169.254.169.254", "192.168.1.1", "::ffff:127.0.0.1", "64:ff9b::a9fe:a9fe"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Fatal("private address allowed", s)
		}
	}
	if _, err := boundedBytes(strings.NewReader(strings.Repeat("x", MaxEvidence+1))); err == nil {
		t.Fatal("unbounded payload")
	}
	for _, raw := range []string{`{"a":{"x":1,"x":2}}`, `{"x":1} {}`, strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34)} {
		var dst any
		if jsonObject([]byte(raw), &dst) == nil {
			t.Fatal("ambiguous JSON accepted", raw)
		}
	}
	var dst struct {
		N int64 `json:"n"`
	}
	if jsonObject([]byte(`{"n":9007199254740993}`), &dst) != nil || dst.N != 9007199254740993 {
		t.Fatal("integer precision lost")
	}
}
func TestOutboundErrorsDoNotExposeSecrets(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("key=merchant-secret") })}
	_, err := requestJSON(context.Background(), client, "GET", "https://pay.example.com/api.php?key=merchant-secret", nil, http.Header{})
	if err == nil || strings.Contains(err.Error(), "merchant-secret") {
		t.Fatal("secret leaked", err)
	}
}
