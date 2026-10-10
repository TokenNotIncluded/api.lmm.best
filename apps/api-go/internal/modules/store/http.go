package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

func (*Service) Name() string { return "store" }
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /catalog", func(w http.ResponseWriter, r *http.Request) {
		var c CatalogChange
		if err := Decode(w, r, &c); err != nil {
			Respond(w, nil, err)
			return
		}
		token, err := Credential(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.Catalog(r.Context(), token, c)
		Respond(w, v, err)
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var p Purchase
		if err := Decode(w, r, &p); err != nil {
			Respond(w, nil, err)
			return
		}
		token, err := Credential(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.Purchase(r.Context(), token, p)
		Respond(w, v, err)
	})
	mux.HandleFunc("POST /orders/change", func(w http.ResponseWriter, r *http.Request) {
		var c OrderChange
		if err := Decode(w, r, &c); err != nil {
			Respond(w, nil, err)
			return
		}
		token, err := Credential(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.ChangeOrder(r.Context(), token, c)
		Respond(w, v, err)
	})
	mux.HandleFunc("GET /shops/{shop}/orders/{order}", func(w http.ResponseWriter, r *http.Request) {
		token, err := Credential(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.GetOrder(r.Context(), token, r.PathValue("shop"), r.PathValue("order"))
		Respond(w, v, err)
	})
	mux.HandleFunc("GET /shops/{shop}/products", func(w http.ResponseWriter, r *http.Request) {
		after, limit, err := Pagination(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.Browse(r.Context(), r.PathValue("shop"), after, limit)
		Respond(w, v, err)
	})
	mux.HandleFunc("GET /shops/{shop}/variants", func(w http.ResponseWriter, r *http.Request) {
		after, limit, err := Pagination(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.Variants(r.Context(), r.PathValue("shop"), after, limit)
		Respond(w, v, err)
	})
	mux.HandleFunc("GET /shops/{shop}/manage/{kind}", func(w http.ResponseWriter, r *http.Request) {
		token, err := Credential(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		after, limit, err := Pagination(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		v, err := s.ManageList(r.Context(), token, r.PathValue("shop"), r.PathValue("kind"), after, limit)
		Respond(w, v, err)
	})
	return BoundedHTTP(mux)
}

// These transport helpers are shared with support, not with the public gateway.
// The host strips its own service credential before invoking either module.
func Credential(r *http.Request) (string, error) {
	v := r.Header.Values("X-LMM-User-Credential")
	if len(v) != 1 || len(v[0]) < 32 || len(v[0]) > 256 {
		return "", ErrUnauthorized
	}
	for _, c := range v[0] {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return "", ErrUnauthorized
		}
	}
	return v[0], nil
}
func Decode(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrInvalid
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func Pagination(r *http.Request) (string, int, error) {
	q := r.URL.Query()
	for _, k := range []string{"after", "limit"} {
		if len(q[k]) > 1 {
			return "", 0, ErrInvalid
		}
	}
	after := q.Get("after")
	if len(after) > 128 {
		return "", 0, ErrInvalid
	}
	limit := 50
	if q.Has("limit") {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || !Page(limit) {
			return "", 0, ErrInvalid
		}
	}
	return after, limit, nil
}
func Respond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status, label := 200, ""
	if err != nil {
		status, label = 503, ErrUnavailable.Error()
		switch {
		case errors.Is(err, ErrUnauthorized):
			status, label = 401, ErrUnauthorized.Error()
		case errors.Is(err, ErrForbidden):
			status, label = 403, ErrForbidden.Error()
		case errors.Is(err, ErrNotFound):
			status, label = 404, ErrNotFound.Error()
		case errors.Is(err, ErrInvalid):
			status, label = 400, ErrInvalid.Error()
		case errors.Is(err, ErrConflict):
			status, label = 409, ErrConflict.Error()
		case errors.Is(err, ErrSoldOut):
			status, label = 409, ErrSoldOut.Error()
		case errors.Is(err, ErrPaymentUnavailable):
			status, label = 503, ErrPaymentUnavailable.Error()
		case errors.Is(err, ErrPending):
			status, label = 202, ErrPending.Error()
		}
		if status != 202 {
			value = nil
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Data  any    `json:"data,omitempty"`
		Error string `json:"error,omitempty"`
	}{value, label})
}
func BoundedHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
