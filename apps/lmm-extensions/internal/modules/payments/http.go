package payments

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

type orderView struct {
	ID                  string `json:"id"`
	AccountID           string `json:"account_id"`
	Currency            string `json:"currency"`
	AmountMinor         int64  `json:"amount_minor"`
	State               string `json:"state"`
	ProviderPaid        bool   `json:"provider_paid"`
	CoreCreditConfirmed bool   `json:"core_credit_confirmed"`
	CheckoutURL         string `json:"checkout_url,omitempty"`
}

func publicOrder(o Order) orderView {
	return orderView{o.ID, strconv.FormatInt(o.Intent.AccountID, 10), o.Intent.Currency, o.Intent.AmountMinor, o.State(), o.ProviderPaid, o.Credited(), o.Checkout.URL}
}
func userCredential(r *http.Request) (string, error) {
	values := r.Header.Values("X-LMM-User-Credential")
	if len(values) != 1 || len(values[0]) < 32 || len(values[0]) > 256 {
		return "", ErrDenied
	}
	for _, c := range values[0] {
		if c < 33 || c > 126 {
			return "", ErrDenied
		}
	}
	return values[0], nil
}

// Handler is mounted through modules.New. Host credentials never replace the
// separate user credential, and no account ID header is trusted.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		credential, err := userCredential(r)
		if err != nil {
			paymentError(w, err)
			return
		}
		var body struct {
			AccountID int64  `json:"account_id"`
			ChannelID int64  `json:"channel_id"`
			Currency  string `json:"currency"`
			Amount    int64  `json:"amount_minor"`
		}
		if decodeUser(w, r, &body) != nil || len(r.Header.Values("Idempotency-Key")) != 1 {
			paymentError(w, ErrInvalid)
			return
		}
		o, err := s.Create(r.Context(), credential, PrepareRequest{Key: r.Header.Get("Idempotency-Key"), AccountID: body.AccountID, ChannelID: body.ChannelID, Currency: body.Currency, AmountMinor: body.Amount})
		if err != nil {
			paymentError(w, err)
			return
		}
		paymentJSON(w, http.StatusAccepted, publicOrder(o))
	})
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		credential, err := userCredential(r)
		if err != nil {
			paymentError(w, err)
			return
		}
		o, err := s.Get(r.Context(), credential, r.PathValue("id"))
		if err != nil {
			paymentError(w, err)
			return
		}
		paymentJSON(w, http.StatusOK, publicOrder(o))
	})
	mux.HandleFunc("POST /orders/{id}/refunds", func(w http.ResponseWriter, r *http.Request) {
		credential, err := userCredential(r)
		if err != nil {
			paymentError(w, err)
			return
		}
		var body struct {
			Amount int64 `json:"amount_minor"`
		}
		if decodeUser(w, r, &body) != nil || len(r.Header.Values("Idempotency-Key")) != 1 {
			paymentError(w, ErrInvalid)
			return
		}
		refund, err := s.RequestRefund(r.Context(), credential, r.PathValue("id"), r.Header.Get("Idempotency-Key"), body.Amount)
		if err != nil {
			paymentError(w, err)
			return
		}
		paymentJSON(w, http.StatusAccepted, struct {
			ID     string `json:"id"`
			State  string `json:"state"`
			Amount int64  `json:"amount_minor"`
		}{refund.ID, refund.State, refund.AmountMinor})
	})
	return mux
}

// WebhookHandler exposes callbacks ONLY, on a separate mount from private user
// routes. Stripe/ePay do not receive the host credential. Apply a request-rate
// limit at the ingress and disable query/body logging on this mount.
func (s *Service) WebhookHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callbacks/{channel}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id, err := strconv.ParseInt(r.PathValue("channel"), 10, 64)
		if err != nil {
			paymentError(w, ErrNotFound)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxEvidence)
		err = s.Receive(r.Context(), id, r)
		if err != nil && !errors.Is(err, ErrUnsupported) {
			paymentError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if a := s.adapters[id]; a != nil && a.Channel().Provider == "epay" {
			_, _ = io.WriteString(w, "success")
		} else {
			_, _ = io.WriteString(w, "received")
		}
	})
	return mux
}
func decodeUser(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	b, err := boundedBytes(r.Body)
	if err != nil {
		return err
	}
	var check any
	if jsonObject(b, &check) != nil {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func paymentError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, ErrInvalid):
		status = 400
	case errors.Is(err, ErrDenied):
		status = 403
	case errors.Is(err, ErrNotFound):
		status = 404
	case errors.Is(err, ErrConflict):
		status = 409
	case errors.Is(err, ErrUnsupported):
		status = 422
	}
	paymentJSON(w, status, map[string]string{"error": errorCode(err)})
}
func paymentJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
