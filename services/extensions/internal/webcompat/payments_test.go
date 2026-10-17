// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

type paymentStatusFake struct {
	token string
	user  int64
	id    string
	body  PaymentPublicStatus
	err   error
}

func (f *paymentStatusFake) Status(_ context.Context, sessionToken string, userID int64, orderID string) (PaymentPublicStatus, error) {
	f.token, f.user, f.id = sessionToken, userID, orderID
	if f.err != nil {
		return PaymentPublicStatus{}, f.err
	}
	return f.body, nil
}

func paymentMux(t *testing.T, reader PaymentStatusReader) (*http.ServeMux, *authCore) {
	t.Helper()
	core := &authCore{}
	auth, err := NewAuthenticator(core, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterPayments(mux, auth, reader)
	return mux, core
}

func paymentCall(mux *http.ServeMux, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// @mark payments.host-admission role=test
func TestPaymentAdmissionReportsStatusReaderAssembly(t *testing.T) {
	code, label := PaymentAdmission(false)
	if code != http.StatusServiceUnavailable || label != "PAYMENT_DISABLED" {
		t.Fatal(code, label)
	}
	code, label = PaymentAdmission(true)
	if code != http.StatusOK || label != "PAYMENT_READY" {
		t.Fatal(code, label)
	}
}

func TestPaymentStatusRequiresSessionAndRefusesMint(t *testing.T) {
	fake := &paymentStatusFake{body: PaymentPublicStatus{
		OrderID: "top_17", State: "credited", Currency: "USD", AmountMinor: 100,
		ProviderPaid: true, CoreCreditConfirmed: true,
	}}
	mux, _ := paymentMux(t, fake)
	if rec := paymentCall(mux, http.MethodGet, "/api/user/payments/orders/top_17", ""); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code)
	}
	if rec := paymentCall(mux, http.MethodGet, "/api/user/payments/orders/top_17", hostToken); rec.Code != http.StatusUnauthorized || fake.user != 0 {
		t.Fatal("host token read a payment", rec.Code, fake.user)
	}
	rec := paymentCall(mux, http.MethodPost, "/api/user/payments/orders/top_17", webToken)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "PAYMENT_HOST_CANNOT_MINT") || fake.user != 0 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = paymentCall(mux, http.MethodGet, "/api/user/payments/orders/top_17", webToken)
	if rec.Code != http.StatusOK || fake.user != 17 || fake.token != webToken || fake.id != "top_17" || !strings.Contains(rec.Body.String(), `"state":"credited"`) {
		t.Fatal(rec.Code, rec.Body.String(), fake.user)
	}
}

func TestPaymentStatusHidesOtherUsersAndUnassembledReader(t *testing.T) {
	fake := &paymentStatusFake{err: payments.ErrDenied}
	mux, _ := paymentMux(t, fake)
	rec := paymentCall(mux, http.MethodGet, "/api/user/payments/orders/top_9", webToken)
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusForbidden || !strings.Contains(string(body), "PAYMENT_ACCESS_DENIED") {
		t.Fatal(rec.Code, string(body))
	}
	bare := http.NewServeMux()
	auth, err := NewAuthenticator(&authCore{}, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	RegisterPayments(bare, auth, nil)
	rec = paymentCall(bare, http.MethodGet, "/api/user/payments/orders/top_9", webToken)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "PAYMENT_DISABLED") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
