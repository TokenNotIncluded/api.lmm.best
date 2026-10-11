package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type StripeConfig struct {
	Channel        Channel
	APIKey         string
	APIVersion     string   // Pin a tested Stripe API version; do not follow a changing default.
	WebhookSecrets []string // Current and previous secret during rotation.
	SuccessURL     string
	CancelURL      string
}
type Stripe struct {
	cfg    StripeConfig
	client *http.Client
}

func NewStripe(cfg StripeConfig) (*Stripe, error) {
	if !cfg.Channel.valid() || cfg.Channel.Provider != "stripe" || !strings.HasPrefix(cfg.Channel.Merchant, "acct_") || len(cfg.WebhookSecrets) < 1 || len(cfg.WebhookSecrets) > 2 || len(cfg.APIVersion) < 10 || len(cfg.APIVersion) > 64 {
		return nil, ErrInvalid
	}
	for _, r := range cfg.APIVersion {
		if r < 33 || r > 126 {
			return nil, ErrInvalid
		}
	}
	prefix := "sk_" + cfg.Channel.Environment + "_"
	restricted := "rk_" + cfg.Channel.Environment + "_"
	if !strings.HasPrefix(cfg.APIKey, prefix) && !strings.HasPrefix(cfg.APIKey, restricted) {
		return nil, ErrInvalid
	}
	for _, secret := range cfg.WebhookSecrets {
		if !strings.HasPrefix(secret, "whsec_") || len(secret) < 16 {
			return nil, ErrInvalid
		}
	}
	if _, err := secureURL(cfg.SuccessURL); err != nil {
		return nil, err
	}
	if _, err := secureURL(cfg.CancelURL); err != nil {
		return nil, err
	}
	cfg.WebhookSecrets = append([]string(nil), cfg.WebhookSecrets...)
	return &Stripe{cfg: cfg, client: outboundClient()}, nil
}
func (s *Stripe) Channel() Channel                { return s.cfg.Channel }
func (*Stripe) RefundEnabled() bool               { return true }
func (*Stripe) PartialRefunds() bool              { return true }
func (*Stripe) ReplayWindow(string) time.Duration { return 23 * time.Hour }
func (s *Stripe) api(ctx context.Context, method, path, key string, form url.Values) ([]byte, error) {
	h := http.Header{"Authorization": {"Bearer " + s.cfg.APIKey}, "Stripe-Version": {s.cfg.APIVersion}}
	if key != "" {
		h.Set("Idempotency-Key", key)
	}
	return requestJSON(ctx, s.client, method, "https://api.stripe.com/v1/"+path, form, h)
}
func (s *Stripe) merchant(ctx context.Context) error {
	b, err := s.api(ctx, "GET", "account", "", nil)
	if err != nil {
		return err
	}
	var a struct {
		ID string `json:"id"`
	}
	if jsonObject(b, &a) != nil || a.ID != s.cfg.Channel.Merchant {
		return ErrDenied
	}
	return nil
}

type stripeObject struct {
	ID            string            `json:"id"`
	Object        string            `json:"object"`
	Status        string            `json:"status"`
	Mode          string            `json:"mode"`
	Amount        int64             `json:"amount"`
	Received      int64             `json:"amount_received"`
	Total         int64             `json:"amount_total"`
	Currency      string            `json:"currency"`
	Livemode      *bool             `json:"livemode"`
	PaymentIntent json.RawMessage   `json:"payment_intent"`
	Metadata      map[string]string `json:"metadata"`
	URL           string            `json:"url"`
	Reference     string            `json:"client_reference_id"`
}
type stripeEnvelope struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Account  string `json:"account"`
	Livemode *bool  `json:"livemode"`
	Data     struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

func refID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &v) == nil {
		return v.ID
	}
	return ""
}

// Creation and recovery must agree on the immutable wallet checkout fields.
// A subscription/setup session is not evidence for a one-time wallet top-up.
func (s *Stripe) checkoutMatches(v stripeObject, o Order) bool {
	if !validID(v.ID) || !strings.HasPrefix(v.ID, "cs_") || v.Object != "checkout.session" || v.Mode != "payment" ||
		v.Reference != o.ID || v.Metadata["lmm_order_id"] != o.ID || v.Total != o.Intent.AmountMinor ||
		strings.ToUpper(v.Currency) != o.Intent.Currency || v.Livemode == nil ||
		*v.Livemode != (s.cfg.Channel.Environment == "live") {
		return false
	}
	// Checkout can legitimately have no PaymentIntent before payment begins.
	// A present but malformed reference is not the same as a pending payment.
	if len(v.PaymentIntent) == 0 || bytes.Equal(bytes.TrimSpace(v.PaymentIntent), []byte("null")) {
		return true
	}
	id := refID(v.PaymentIntent)
	return validID(id) && strings.HasPrefix(id, "pi_")
}

func (s *Stripe) Checkout(ctx context.Context, o Order, key string) (Checkout, error) {
	// Only the immutable intent returned by Rust may bind provider evidence.
	// Reject an absent intent or a different merchant before any provider I/O.
	if o.Intent.ID <= 0 || o.Intent.Merchant != s.cfg.Channel.Merchant {
		return Checkout{}, ErrInvalid
	}
	if err := s.merchant(ctx); err != nil {
		return Checkout{}, err
	}
	f := url.Values{"mode": {"payment"}, "success_url": {s.cfg.SuccessURL}, "cancel_url": {s.cfg.CancelURL}, "client_reference_id": {o.ID},
		"metadata[lmm_order_id]": {o.ID}, "payment_intent_data[metadata][lmm_order_id]": {o.ID},
		"metadata[lmm_core_intent_id]": {strconv.FormatInt(o.Intent.ID, 10)}, "metadata[lmm_merchant]": {o.Intent.Merchant},
		"payment_intent_data[metadata][lmm_core_intent_id]": {strconv.FormatInt(o.Intent.ID, 10)}, "payment_intent_data[metadata][lmm_merchant]": {o.Intent.Merchant},
		"line_items[0][quantity]": {"1"}, "line_items[0][price_data][currency]": {strings.ToLower(o.Intent.Currency)},
		"line_items[0][price_data][unit_amount]": {strconv.FormatInt(o.Intent.AmountMinor, 10)}, "line_items[0][price_data][product_data][name]": {"Account top-up"},
		"automatic_tax[enabled]": {"false"}, "allow_promotion_codes": {"false"}, "adaptive_pricing[enabled]": {"false"}}
	b, err := s.api(ctx, "POST", "checkout/sessions", key, f)
	if err != nil {
		return Checkout{}, err
	}
	var v stripeObject
	if jsonObject(b, &v) != nil || !s.checkoutMatches(v, o) {
		return Checkout{}, ErrConflict
	}
	u, err := secureURL(v.URL)
	if err != nil || u.Hostname() != "checkout.stripe.com" {
		return Checkout{}, ErrConflict
	}
	return Checkout{ID: v.ID, URL: v.URL, PaymentID: refID(v.PaymentIntent)}, nil
}
func (s *Stripe) Verify(r *http.Request, now time.Time) (Event, error) {
	if r.Method != "POST" || len(r.Header.Values("Stripe-Signature")) != 1 {
		return Event{}, ErrInvalid
	}
	raw, err := boundedBytes(r.Body)
	if err != nil {
		return Event{}, err
	}
	signature := r.Header.Get("Stripe-Signature")
	if !stripeSignature(raw, signature, s.cfg.WebhookSecrets, now) {
		return Event{}, ErrDenied
	}
	e, err := s.parseEnvelope(raw)
	if err != nil {
		return Event{}, err
	}
	// Direct-account events omit account. Confirm the signed event also exists in
	// the API-key's configured merchant, instead of trusting metadata as identity.
	if err = s.merchant(r.Context()); err != nil {
		return Event{}, err
	}
	retrieved, err := s.api(r.Context(), "GET", "events/"+url.PathEscape(e.Evidence.EventID), "", nil)
	if err != nil {
		return Event{}, err
	}
	remote, err := s.parseEnvelope(retrieved)
	if err != nil || eventDigest(remote) != eventDigest(e) || remote.Evidence.EventID != e.Evidence.EventID {
		return Event{}, ErrConflict
	}
	// Refund metadata cannot replace the original payment's order binding.
	// Check it even when the refund already contains an order reference.
	if e.RefundID != "" || e.OrderID == "" {
		v, _, err := s.payment(r.Context(), e.Evidence.Transaction)
		if err != nil {
			return Event{}, err
		}
		orderID := v.Metadata["lmm_order_id"]
		if !validID(orderID) || e.OrderID != "" && e.OrderID != orderID ||
			e.Currency != strings.ToUpper(v.Currency) || e.AmountMinor > v.Amount {
			return Event{}, ErrConflict
		}
		e.OrderID = orderID
	}
	if e.OrderID == "" {
		return Event{}, ErrConflict
	}
	e.Evidence.Signature = []byte(signature)
	return e, nil
}
func stripeSignature(payload []byte, header string, secrets []string, now time.Time) bool {
	if len(header) > 2048 {
		return false
	}
	var timestamp string
	var signatures [][]byte
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return false
		}
		if k == "t" {
			if timestamp != "" {
				return false
			}
			timestamp = v
		}
		if k == "v1" {
			b, err := hex.DecodeString(v)
			if err != nil || len(b) != sha256.Size {
				return false
			}
			signatures = append(signatures, b)
		}
	}
	t, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || t <= 0 {
		return false
	}
	age := now.Sub(time.Unix(t, 0))
	if age > 5*time.Minute || age < -5*time.Minute {
		return false
	}
	for _, secret := range secrets {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(timestamp + "."))
		m.Write(payload)
		want := m.Sum(nil)
		for _, sig := range signatures {
			if hmac.Equal(want, sig) {
				return true
			}
		}
	}
	return false
}
func (s *Stripe) parseEnvelope(raw []byte) (Event, error) {
	var envelope stripeEnvelope
	if jsonObject(raw, &envelope) != nil || !validID(envelope.ID) || envelope.Livemode == nil || *envelope.Livemode != (s.cfg.Channel.Environment == "live") || envelope.Account != "" {
		return Event{}, ErrInvalid
	}
	e, err := s.observation(envelope.Data.Object, envelope.Type)
	if err != nil {
		return Event{}, err
	}
	e.Evidence.EventID = envelope.ID
	e.Evidence.Payload = append([]byte(nil), raw...)
	e.Evidence.Source = "webhook"
	return e, nil
}
func (s *Stripe) observation(raw []byte, kind string) (Event, error) {
	var v stripeObject
	if jsonObject(raw, &v) != nil {
		return Event{}, ErrInvalid
	}
	c := s.cfg.Channel
	e := Event{OrderID: v.Metadata["lmm_order_id"], Currency: strings.ToUpper(v.Currency), AmountMinor: v.Amount,
		Evidence: Evidence{Provider: c.Provider, Merchant: c.Merchant, Environment: c.Environment, Payload: append([]byte(nil), raw...)}}
	switch kind {
	case "payment_intent.succeeded", "payment_intent.payment_failed":
		if v.Object != "payment_intent" || !strings.HasPrefix(v.ID, "pi_") {
			return Event{}, ErrInvalid
		}
		e.Evidence.Transaction = v.ID
		e.Type = PaymentFailed
		if kind == "payment_intent.succeeded" {
			if v.Status != "succeeded" || v.Received != v.Amount {
				return Event{}, ErrConflict
			}
			e.Type = Paid
		}
	case "refund.created", "refund.updated", "refund.failed":
		if v.Object != "refund" || !strings.HasPrefix(v.ID, "re_") {
			return Event{}, ErrInvalid
		}
		e.Evidence.Transaction = refID(v.PaymentIntent)
		e.RefundID = v.ID
		e.LocalRefundID = v.Metadata["lmm_refund_id"]
		switch v.Status {
		case "succeeded":
			e.Type = RefundSucceeded
		case "failed", "canceled":
			e.Type = RefundFailed
		case "pending", "requires_action":
			e.Type = RefundPending
		default:
			return Event{}, ErrInvalid
		}
	default:
		return Event{}, ErrUnsupported
	}
	if e.Currency != c.Currency || !validAmount(e.AmountMinor) || !validID(e.Evidence.Transaction) {
		return Event{}, ErrInvalid
	}
	return e, nil
}
func (s *Stripe) payment(ctx context.Context, id string) (stripeObject, []byte, error) {
	if !validID(id) || !strings.HasPrefix(id, "pi_") {
		return stripeObject{}, nil, ErrInvalid
	}
	b, err := s.api(ctx, "GET", "payment_intents/"+id, "", nil)
	if err != nil {
		return stripeObject{}, nil, err
	}
	var v stripeObject
	if jsonObject(b, &v) != nil || v.ID != id || v.Object != "payment_intent" || v.Livemode == nil || *v.Livemode != (s.cfg.Channel.Environment == "live") {
		return v, nil, ErrConflict
	}
	return v, b, nil
}
func (s *Stripe) paymentForOrder(ctx context.Context, id string, o Order) (stripeObject, []byte, error) {
	v, raw, err := s.payment(ctx, id)
	if err != nil {
		return stripeObject{}, nil, err
	}
	if v.Metadata["lmm_order_id"] != o.ID || v.Amount != o.Intent.AmountMinor || strings.ToUpper(v.Currency) != o.Intent.Currency {
		return stripeObject{}, nil, ErrConflict
	}
	return v, raw, nil
}

func (s *Stripe) Lookup(ctx context.Context, o Order) ([]Event, error) {
	if err := s.merchant(ctx); err != nil {
		return nil, err
	}
	paymentID := o.PaymentID
	if paymentID == "" {
		if o.Checkout.ID == "" {
			return nil, ErrNotFound
		}
		b, err := s.api(ctx, "GET", "checkout/sessions/"+url.PathEscape(o.Checkout.ID), "", nil)
		if err != nil {
			return nil, err
		}
		var v stripeObject
		if jsonObject(b, &v) != nil || v.ID != o.Checkout.ID || !s.checkoutMatches(v, o) {
			return nil, ErrConflict
		}
		paymentID = refID(v.PaymentIntent)
		if paymentID == "" {
			return nil, nil
		}
	}
	v, raw, err := s.paymentForOrder(ctx, paymentID, o)
	if err != nil {
		return nil, err
	}
	var events []Event
	if v.Status == "succeeded" {
		e, err := s.observation(raw, "payment_intent.succeeded")
		if err != nil {
			return nil, err
		}
		apiEvidence(&e, v.ID, v.Status)
		events = append(events, e)
	}
	cursor := ""
	// Bounded pagination: never assume the embedded charge.refunds list is complete.
	for page := 0; page < 10; page++ {
		q := url.Values{"payment_intent": {paymentID}, "limit": {"100"}}
		if cursor != "" {
			q.Set("starting_after", cursor)
		}
		raw, err := s.api(ctx, "GET", "refunds?"+q.Encode(), "", nil)
		if err != nil {
			return nil, err
		}
		var list struct {
			Data []json.RawMessage `json:"data"`
			More bool              `json:"has_more"`
		}
		if jsonObject(raw, &list) != nil {
			return nil, ErrInvalid
		}
		for _, item := range list.Data {
			e, err := s.observation(item, "refund.updated")
			if err != nil {
				return nil, err
			}
			if e.Evidence.Transaction != paymentID || e.OrderID != "" && e.OrderID != o.ID {
				return nil, ErrConflict
			}
			e.OrderID = o.ID
			apiEvidence(&e, e.RefundID, e.Type)
			events = append(events, e)
			cursor = e.RefundID
		}
		if !list.More {
			return events, nil
		}
		if len(list.Data) == 0 {
			return nil, ErrConflict
		}
	}
	return nil, ErrUnavailable // Incomplete reconciliation is never reported complete.
}
func apiEvidence(e *Event, id, state string) {
	e.Evidence.Source = "api"
	e.Evidence.EventID = stableKey("api", id, state)
	e.Evidence.Signature = nil
}
func (s *Stripe) Refund(ctx context.Context, o Order, r Refund, key string) (RefundResult, error) {
	if o.Intent.ID <= 0 || o.Intent.Merchant != s.cfg.Channel.Merchant || r.HoldReceipt == "" || !validAmount(r.AmountMinor) || r.AmountMinor > o.Intent.AmountMinor || o.PaymentID == "" {
		return RefundResult{}, ErrInvalid
	}
	if err := s.merchant(ctx); err != nil {
		return RefundResult{}, err
	}
	// Verify the saved transaction before the provider mutation. A timeout or
	// conflicting binding leaves the Rust hold intact for reconciliation.
	payment, _, err := s.paymentForOrder(ctx, o.PaymentID, o)
	if err != nil {
		return RefundResult{}, err
	}
	if payment.Status != "succeeded" || payment.Received != payment.Amount {
		return RefundResult{}, ErrConflict
	}
	f := url.Values{"payment_intent": {o.PaymentID}, "amount": {strconv.FormatInt(r.AmountMinor, 10)},
		"metadata[lmm_order_id]": {o.ID}, "metadata[lmm_refund_id]": {r.ID},
		"metadata[lmm_core_intent_id]": {strconv.FormatInt(o.Intent.ID, 10)}, "metadata[lmm_merchant]": {o.Intent.Merchant},
		"metadata[lmm_hold_receipt_id]": {r.HoldReceipt}}
	b, err := s.api(ctx, "POST", "refunds", key, f)
	if err != nil {
		return RefundResult{}, err
	}
	var v stripeObject
	if jsonObject(b, &v) != nil || !validID(v.ID) || !strings.HasPrefix(v.ID, "re_") || v.Object != "refund" || refID(v.PaymentIntent) != o.PaymentID || v.Amount != r.AmountMinor || strings.ToUpper(v.Currency) != o.Intent.Currency || v.Metadata["lmm_order_id"] != o.ID || v.Metadata["lmm_refund_id"] != r.ID {
		return RefundResult{}, ErrConflict
	}
	return RefundResult{ID: v.ID, Status: v.Status}, nil
}
