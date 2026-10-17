// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// PaymentPublicStatus is the wallet read model. It is not a balance and it
// cannot create a credit or a refund.
type PaymentPublicStatus struct {
	OrderID             string `json:"order_id"`
	State               string `json:"state"`
	Currency            string `json:"currency"`
	AmountMinor         int64  `json:"amount_minor"`
	ProviderPaid        bool   `json:"provider_paid"`
	CoreCreditConfirmed bool   `json:"core_credit_confirmed"`
	RefundDispatch      string `json:"refund_dispatch,omitempty"`
}

// PaymentStatusReader reads one order for the verified web session. sessionToken
// is the bearer Core already accepted. Implementations must pass that token to
// Core and must not substitute the extension host token.
type PaymentStatusReader interface {
	Status(ctx context.Context, sessionToken string, userID int64, orderID string) (PaymentPublicStatus, error)
}

// NewPaymentStatusReader exposes only the authorized order projection. The
// service rechecks the current session against Rust, including team permissions;
// the order creator is never a substitute for account authorization.
func NewPaymentStatusReader(service *payments.Service) PaymentStatusReader {
	if service == nil {
		return nil
	}
	return paymentStatusReader{service: service}
}

type paymentStatusReader struct{ service *payments.Service }

func (p paymentStatusReader) Status(ctx context.Context, sessionToken string, userID int64, orderID string) (PaymentPublicStatus, error) {
	if sessionToken == "" || userID <= 0 {
		return PaymentPublicStatus{}, payments.ErrDenied
	}
	o, err := p.service.Get(ctx, sessionToken, orderID)
	if err != nil {
		return PaymentPublicStatus{}, err
	}
	// The storage snapshot is a read model, not authority. Refuse a corrupt
	// projection even after Rust has checked access to the referenced intent.
	if o.ID != orderID || o.Intent.ID <= 0 || orderID != "top_"+strconv.FormatInt(o.Intent.ID, 10) ||
		o.Intent.AmountMinor <= 0 || !validPaymentCurrency(o.Intent.Currency) {
		return PaymentPublicStatus{}, payments.ErrUnavailable
	}
	status := PaymentPublicStatus{
		OrderID: o.ID, State: o.State(), Currency: o.Intent.Currency,
		AmountMinor: o.Intent.AmountMinor, ProviderPaid: o.ProviderPaid,
		CoreCreditConfirmed: o.Credited(),
	}
	for _, refund := range o.Refunds {
		// Map iteration must never let a responded refund hide another refund
		// whose provider write still has an unknown outcome.
		if refund.Dispatch == payments.DispatchUnknown {
			status.RefundDispatch = payments.DispatchUnknown
			break
		}
		if refund.Dispatch == payments.DispatchResponded {
			status.RefundDispatch = payments.DispatchResponded
		}
	}
	return status, nil
}

// PaymentAdmission reports whether the status reader is assembled. It does not
// certify payment writes, worker execution or financial readiness.
// @mark payments.host-admission role=authority
func PaymentAdmission(assembled bool) (int, string) {
	if !assembled {
		return http.StatusServiceUnavailable, "PAYMENT_DISABLED"
	}
	return http.StatusOK, "PAYMENT_READY"
}

// RegisterPayments mounts a read-only status route. Mutating methods are refused
// here even for a signed-in user; checkout and refund stay on the payment module.
func RegisterPayments(mux *http.ServeMux, auth *Authenticator, reader PaymentStatusReader) {
	mux.HandleFunc("/api/user/payments/orders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writePayment(w, http.StatusForbidden, "PAYMENT_HOST_CANNOT_MINT", "This route cannot create a credit or a refund.")
			return
		}
		self, err := auth.Verify(r.Context(), r)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writePayment(w, http.StatusUnauthorized, "PAYMENT_LOGIN_REQUIRED", "Sign in before reading this payment.")
			return
		}
		code, label := PaymentAdmission(reader != nil && !store.Missing(reader))
		if code != http.StatusOK {
			writePayment(w, code, label, "Payment status is not assembled.")
			return
		}
		orderID := strings.TrimPrefix(r.URL.Path, "/api/user/payments/orders/")
		if r.URL.Path == "/api/user/payments/orders/" || strings.Contains(orderID, "/") || !validPaymentOrderID(orderID) {
			writePayment(w, http.StatusNotFound, "PAYMENT_NOT_FOUND", "The payment order was not found.")
			return
		}
		status, err := reader.Status(r.Context(), token, self.GetUserId(), orderID)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		if status.OrderID != orderID || status.AmountMinor <= 0 || !validPaymentCurrency(status.Currency) {
			writePayment(w, http.StatusServiceUnavailable, "PAYMENT_UNAVAILABLE", "Payment status could not be read.")
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			return
		}
		writePaymentData(w, status)
	})
}

func validPaymentCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, c := range currency {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func validPaymentOrderID(id string) bool {
	if len(id) < 4 || len(id) > 80 || !strings.HasPrefix(id, "top_") {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func writePaymentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		writePayment(w, http.StatusUnauthorized, "PAYMENT_LOGIN_REQUIRED", "Sign in before reading this payment.")
	case errors.Is(err, payments.ErrDenied), errors.Is(err, store.ErrForbidden):
		writePayment(w, http.StatusForbidden, "PAYMENT_ACCESS_DENIED", "This payment belongs to another account.")
	case errors.Is(err, payments.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writePayment(w, http.StatusNotFound, "PAYMENT_NOT_FOUND", "The payment order was not found.")
	case errors.Is(err, payments.ErrConflict), errors.Is(err, store.ErrConflict):
		writePayment(w, http.StatusConflict, "PAYMENT_CONFLICT", "Read the current payment before trying again.")
	default:
		writePayment(w, http.StatusServiceUnavailable, "PAYMENT_UNAVAILABLE", "Payment status could not be read.")
	}
}

func writePayment(w http.ResponseWriter, code int, label, message string) {
	body, err := json.Marshal(map[string]any{"success": false, "code": label, "message": message})
	if err != nil {
		code = http.StatusServiceUnavailable
		body = []byte(`{"success":false,"code":"PAYMENT_UNAVAILABLE","message":"Payment status could not be read."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(append(body, '\n'))
}

func writePaymentData(w http.ResponseWriter, status PaymentPublicStatus) {
	body, err := json.Marshal(map[string]any{"success": true, "message": "", "data": status})
	if err != nil {
		writePayment(w, http.StatusServiceUnavailable, "PAYMENT_UNAVAILABLE", "Payment status could not be read.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}
