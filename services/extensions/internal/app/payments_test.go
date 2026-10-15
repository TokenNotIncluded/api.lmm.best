// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

func TestPaymentsTypedConfig(t *testing.T) {
	base := "version=1\n[host]\ntoken_file='/tmp/host-token'\nmodules=['payments']\n[core]\nsocket='/tmp/core.sock'\nidentity_token_file='/tmp/identity-token'\npayment_token_file='/tmp/payment-token'\n[payments]\ndatabase_name='lmm_payments_test'\ndatabase_url_file='/tmp/payment.url'\nconfig_file='/tmp/payment.toml'\ncallbacks_enabled=true\n"
	write := func(raw string) string {
		path := filepath.Join(t.TempDir(), "extensions.toml")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cfg, err := LoadConfig(write(base))
	if err != nil || cfg.Core.PaymentTokenFile != "/tmp/payment-token" || cfg.Payments.DatabaseName != "lmm_payments_test" || !cfg.Payments.CallbacksEnabled {
		t.Fatal(cfg, err)
	}
	for _, raw := range []string{
		strings.Replace(base, "modules=['payments']", "modules=['identity']", 1),
		strings.Replace(base, "payment_token_file=", "PaymentTokenFile=", 1),
		strings.Replace(base, "[payments]", "[Payments]", 1),
		strings.Replace(base, "'/tmp/payment-token'", "'/tmp/identity-token'", 1),
		strings.Replace(base, "'/tmp/payment-token'", "'/tmp/host-token'", 1),
		strings.Replace(base, "'/tmp/payment.toml'", "'relative'", 1),
		strings.Replace(base, "config_file='/tmp/payment.toml'\n", "", 1),
		strings.Replace(base, "database_name='lmm_payments_test'\n", "", 1),
		base + "endpoint='https://unreviewed.example.test'\n",
	} {
		if _, err := LoadConfig(write(raw)); err == nil {
			t.Fatal("invalid payment configuration accepted")
		}
	}
}
func paymentIngressHost(t *testing.T) *modules.Host {
	t.Helper()
	host, err := modules.NewHost(nil, []byte("synthetic_payment_ingress_host_0000001"))
	if err != nil {
		t.Fatal(err)
	}
	if err = host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	return host
}
func paymentCallbackRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader("{\"original\":true}"))
	request.Header.Set("Stripe-Signature", "t=123,v1=synthetic")
	return request
}
func TestPaymentCallbacksAreSeparateFromPrivateRoutes(t *testing.T) {
	host := paymentIngressHost(t)
	called := 0
	callbacks := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-LMM-User-Credential") != "" || r.Header.Get("Cookie") != "" {
			t.Error("private credential reached provider ingress")
		}
		if r.Header.Get("Stripe-Signature") != "t=123,v1=synthetic" {
			t.Error("original signature changed")
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Error("callback has no bounded deadline")
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || string(raw) != "{\"original\":true}" {
			t.Error("original callback bytes changed", err)
		}
		w.WriteHeader(200)
	})
	ingress := mountPaymentCallbacks(host, callbacks, host)
	req := paymentCallbackRequest("POST", "/callbacks/1")
	req.Header.Set("Authorization", "Bearer synthetic_payment_ingress_host_0000001")
	req.Header.Set("X-LMM-User-Credential", "synthetic_user_credential_00000000001")
	req.Header.Set("Cookie", "synthetic_session=private")
	w := paymentCallbackRecorder()
	ingress.ServeHTTP(w, req)
	if w.Code != 200 || called != 1 {
		t.Fatal(w.Code, called)
	}
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/callbacks/1", 405}, {"POST", "/callbacks/01", 404}, {"POST", "/callbacks/-1", 404}, {"POST", "/callbacks/1/orders", 404}, {"POST", "/callbacks/%31", 404}, {"POST", "/callbacks/1?secret=private", 404}, {"GET", "/extensions/v1/payments/orders", 401},
	}
	for _, c := range cases {
		w = paymentCallbackRecorder()
		ingress.ServeHTTP(w, paymentCallbackRequest(c.method, c.path))
		if w.Code != c.want {
			t.Fatalf("%s %s: %d want %d", c.method, c.path, w.Code, c.want)
		}
	}
	req = paymentCallbackRequest("POST", "/callbacks/1")
	req.Header.Add("Stripe-Signature", "second")
	w = paymentCallbackRecorder()
	ingress.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("duplicate signature admitted", w.Code)
	}
	req = paymentCallbackRequest("POST", "/callbacks/1")
	req.ContentLength = payments.MaxEvidence + 1
	w = paymentCallbackRecorder()
	ingress.ServeHTTP(w, req)
	if w.Code != 413 || called != 1 {
		t.Fatal("oversize callback admitted", w.Code, called)
	}
}
func TestPaymentCallbacksJoinHostDrain(t *testing.T) {
	host := paymentIngressHost(t)
	admitted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	ingress := mountPaymentCallbacks(host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(admitted); <-release; w.WriteHeader(200) }), host)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ingress.ServeHTTP(paymentCallbackRecorder(), paymentCallbackRequest("POST", "/callbacks/1"))
	}()
	select {
	case <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("callback not admitted")
	}
	host.BeginDrain()
	w := paymentCallbackRecorder()
	ingress.ServeHTTP(w, paymentCallbackRequest("POST", "/callbacks/1"))
	if w.Code != 503 {
		t.Fatal("callback admitted during drain", w.Code)
	}
	bounded, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := host.Wait(bounded); err == nil {
		t.Fatal("host forgot active callback")
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not finish")
	}
	if err := host.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestPaymentCallbacksBoundConcurrencyAndRate(t *testing.T) {
	host := paymentIngressHost(t)
	admitted, release := make(chan struct{}, modules.MaxInFlightPerModule), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	ingress := mountPaymentCallbacks(host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { admitted <- struct{}{}; <-release; w.WriteHeader(200) }), host)
	var wg sync.WaitGroup
	for i := 0; i < modules.MaxInFlightPerModule; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ingress.ServeHTTP(paymentCallbackRecorder(), paymentCallbackRequest("POST", "/callbacks/1"))
		}()
	}
	for i := 0; i < modules.MaxInFlightPerModule; i++ {
		select {
		case <-admitted:
		case <-time.After(3 * time.Second):
			t.Fatal("callback not admitted")
		}
	}
	w := paymentCallbackRecorder()
	ingress.ServeHTTP(w, paymentCallbackRequest("POST", "/callbacks/1"))
	if w.Code != 503 {
		t.Fatal("callback concurrency unbounded", w.Code)
	}
	unblock()
	wg.Wait()
	ingress = mountPaymentCallbacks(host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), host)
	for i := 0; i < 33; i++ {
		w = paymentCallbackRecorder()
		ingress.ServeHTTP(w, paymentCallbackRequest("POST", "/callbacks/1"))
		want := 200
		if i == 32 {
			want = 429
		}
		if w.Code != want {
			t.Fatal("callback rate not bounded", i, w.Code, want)
		}
	}
}

type callbackRecorder struct{ *httptest.ResponseRecorder }

func (*callbackRecorder) SetReadDeadline(time.Time) error { return nil }
func paymentCallbackRecorder() *callbackRecorder          { return &callbackRecorder{httptest.NewRecorder()} }
func TestPaymentCallbacksRejectUnsupportedReadDeadline(t *testing.T) {
	host := paymentIngressHost(t)
	ingress := mountPaymentCallbacks(host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("callback ran without read deadline") }), host)
	w := httptest.NewRecorder()
	ingress.ServeHTTP(w, paymentCallbackRequest("POST", "/callbacks/1"))
	if w.Code != 503 {
		t.Fatal("unbounded reader accepted", w.Code)
	}
}
func TestPaymentCallbackDeadlineInterruptsSlowBody(t *testing.T) {
	host := paymentIngressHost(t)
	reads := make(chan error, 1)
	ingress := mountPaymentCallbacks(host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		reads <- err
		w.WriteHeader(503)
	}), host)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bounded, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
		defer cancel()
		ingress.ServeHTTP(w, r.WithContext(bounded))
	}))
	server.Config.ReadTimeout = 3 * time.Second
	server.Start()
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A complete first chunk and no final chunk leave Body.Read blocked.
	_, err = io.WriteString(conn, "POST /callbacks/1 HTTP/1.1\r\nHost: synthetic.test\r\nStripe-Signature: t=123,v1=synthetic\r\nTransfer-Encoding: chunked\r\n\r\n1\r\n{\r\n")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reads:
		timeout, ok := err.(net.Error)
		if !ok || !timeout.Timeout() {
			t.Fatal("body read did not time out", err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback context expired without interrupting body read")
	}
}

func TestCallbackSharesHostAdmissionBeforeSideEffects(t *testing.T) {
	host, e := modules.NewHostWithLimit(nil, []byte(strings.Repeat("x", 32)), 1)
	if e != nil {
		t.Fatal(e)
	}
	_ = host.MarkReady()
	release, e := host.TryAcquire()
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	handler := mountPaymentCallbacks(host, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }), host)
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/callbacks/1", strings.NewReader("{}"))
		r.Header.Set("Stripe-Signature", "synthetic")
		return r
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request())
	if w.Code != 503 || !strings.Contains(w.Body.String(), "extension_busy") || calls != 0 {
		t.Fatal(w, calls)
	}
	release()
	accepted := paymentCallbackRecorder()
	handler.ServeHTTP(accepted, request())
	if accepted.Code != 204 || calls != 1 {
		t.Fatal("callback capacity did not recover", accepted.Code, calls)
	}
	host.BeginDrain()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request())
	if w.Code != 503 || !strings.Contains(w.Body.String(), "extension_not_accepting") || calls != 1 {
		t.Fatal(w, calls)
	}
}
