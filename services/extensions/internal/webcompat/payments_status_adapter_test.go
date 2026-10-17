package webcompat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

// Development tests exercise the real Service.Get and Mount paths with narrow
// read ports. They do not certify PostgreSQL durability or Rust authorization.
type statusReadStore struct {
	payments.Store
	order payments.Order
	id    string
	calls int
}

func (s *statusReadStore) Get(_ context.Context, id string) (payments.Order, error) {
	s.calls++
	if id != s.id {
		return payments.Order{}, payments.ErrNotFound
	}
	return s.order, nil
}

type statusReadCore struct {
	payments.Core
	credential string
	intent     payments.Intent
	calls      int
	err        error
}

func (c *statusReadCore) Read(_ context.Context, credential string, intent payments.Intent) error {
	c.calls++
	c.credential, c.intent = credential, intent
	return c.err
}
func statusReadFixture(t *testing.T) (*payments.Service, *statusReadStore, *statusReadCore) {
	t.Helper()
	store := &statusReadStore{id: "top_21", order: payments.Order{
		ID: "top_21", Intent: payments.Intent{ID: 21, AccountID: 900, CreatedBy: 999, Currency: "USD", AmountMinor: 100, Key: "private_intent_prepare_key"},
		ProviderPaid: true, CreditState: "confirmed", CreditReceipt: "private_receipt",
		Refunds: map[string]payments.Refund{
			"first":  {ID: "first", AmountMinor: 10, State: "awaiting_evidence", Dispatch: payments.DispatchUnknown},
			"second": {ID: "second", AmountMinor: 10, State: "awaiting_evidence", Dispatch: payments.DispatchResponded},
		},
		Jobs: map[string]payments.Job{"secret": {Evidence: payments.Evidence{Payload: []byte("private_payload"), Signature: []byte("private_signature")}}},
	}}
	core := &statusReadCore{}
	service, err := payments.New(store, core)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, core
}
func TestPaymentStatusAdapterUsesServiceAuthorizationAndMinimalProjection(t *testing.T) {
	service, store, core := statusReadFixture(t)
	reader := NewPaymentStatusReader(service)
	for range 40 {
		got, err := reader.Status(context.Background(), webToken, 17, store.order.ID)
		if err != nil {
			t.Fatal(err)
		}
		if core.credential != webToken || core.intent.AccountID != 900 || core.intent.CreatedBy != 999 || got.RefundDispatch != payments.DispatchUnknown || !got.CoreCreditConfirmed || got.State != "refund_pending" {
			t.Fatal("session/account authorization or unknown dispatch changed", got)
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private_", "account_id", "intent", "provider_id", "evidence", "signature", "checkout_url", webToken} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private payment field leaked", secret)
			}
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != 7 {
			t.Fatal("unexpected public fields", string(raw))
		}
	}
	core.err = payments.ErrDenied
	if _, err := reader.Status(context.Background(), webToken, 17, store.order.ID); !errors.Is(err, payments.ErrDenied) {
		t.Fatal("core denial bypassed", err)
	}
	if NewPaymentStatusReader(nil) != nil {
		t.Fatal("missing service was assembled")
	}
}
func TestPaymentStatusAdapterMountAdmissionAndReadOnlyBoundary(t *testing.T) {
	service, store, core := statusReadFixture(t)
	host, err := modules.NewHostWithLimit(nil, []byte(hostToken), 1)
	if err != nil {
		t.Fatal(err)
	}
	auth := newAuth(t, &authCore{})
	handler := Mount(host, auth, nil, nil, AdditionalServices{Payments: NewPaymentStatusReader(service)})
	call := func(method, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/user/payments/orders/top_21", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := call("GET", webToken); got.Code != 503 || core.calls != 0 || store.calls != 0 {
		t.Fatal("preparing admitted payment", got.Code)
	}
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", hostToken} {
		if got := call("GET", token); got.Code != 401 || core.calls != 0 {
			t.Fatal("host or missing session accepted", got.Code)
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if got := call(method, webToken); got.Code != 403 || !strings.Contains(got.Body.String(), "PAYMENT_HOST_CANNOT_MINT") || core.calls != 0 {
			t.Fatal("write reached service", method, got.Code)
		}
	}
	if got := call("GET", webToken); got.Code != 200 || core.calls != 1 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("real service read not mounted", got.Code)
	}
	if got := call("HEAD", webToken); got.Code != 200 || got.Body.Len() != 0 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("HEAD leaked/cached body", got.Code)
	}
	release, err := host.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	reads := core.calls
	if got := call("GET", webToken); got.Code != 503 || core.calls != reads {
		t.Fatal("saturated host admitted payment", got.Code)
	}
	release()
	host.BeginDrain()
	if got := call("GET", webToken); got.Code != 503 || core.calls != reads {
		t.Fatal("draining host admitted payment", got.Code)
	}

	bareHost, err := modules.NewHost(nil, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	if err := bareHost.MarkReady(); err != nil {
		t.Fatal(err)
	}
	handler = Mount(bareHost, auth, nil, nil)
	if got := call("GET", ""); got.Code != 401 {
		t.Fatal("disabled reader skipped auth", got.Code)
	}
	if got := call("GET", webToken); got.Code != 503 || !strings.Contains(got.Body.String(), "PAYMENT_DISABLED") {
		t.Fatal("missing reader not explicit", got.Code, got.Body.String())
	}
}

func TestPaymentStatusAdapterRefusesCorruptSnapshotsAndMissingActor(t *testing.T) {
	service, store, core := statusReadFixture(t)
	reader := NewPaymentStatusReader(service)
	for _, actor := range []struct {
		token string
		id    int64
	}{{"", 17}, {webToken, 0}, {webToken, -1}} {
		if _, err := reader.Status(context.Background(), actor.token, actor.id, "top_21"); !errors.Is(err, payments.ErrDenied) || store.calls != 0 || core.calls != 0 {
			t.Fatal("missing actor reached payment storage or Core", err)
		}
	}
	original := store.order
	for _, tc := range []struct {
		name    string
		corrupt func(*payments.Order)
	}{
		{"wrong-order", func(o *payments.Order) { o.ID = "top_22" }},
		{"wrong-intent", func(o *payments.Order) { o.Intent.ID = 22 }},
		{"zero-intent", func(o *payments.Order) { o.Intent.ID = 0 }},
		{"zero-amount", func(o *payments.Order) { o.Intent.AmountMinor = 0 }},
		{"negative-amount", func(o *payments.Order) { o.Intent.AmountMinor = -1 }},
		{"lowercase-currency", func(o *payments.Order) { o.Intent.Currency = "usd" }},
		{"nonletter-currency", func(o *payments.Order) { o.Intent.Currency = "U1D" }},
		{"long-currency", func(o *payments.Order) { o.Intent.Currency = "USDD" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.order = original
			tc.corrupt(&store.order)
			if got, err := reader.Status(context.Background(), webToken, 17, "top_21"); !errors.Is(err, payments.ErrUnavailable) || got != (PaymentPublicStatus{}) {
				t.Fatal("corrupt snapshot escaped", got, err)
			}
		})
	}
}
