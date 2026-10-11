// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type StoreService interface {
	PublicCatalogue(context.Context, int64, string, int, int) ([]map[string]any, error)
	PublicProduct(context.Context, string) (map[string]any, error)
	LocateOrder(context.Context, string) (store.LocatedOrder, error)
	GetOrder(context.Context, string, string, string) (store.Order, error)
}

// Public catalogue routes retain main's anonymous public browsing. A supplied
// credential must be valid; anonymous access never expands snapshot visibility.
func RegisterStore(mux *http.ServeMux, auth *Authenticator, service StoreService) {
	public := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			for key := range r.Header {
				if strings.EqualFold(key, "Authorization") {
					if _, err := auth.Verify(r.Context(), r); err != nil {
						Respond(w, nil, err)
						return
					}
					break
				}
			}
			if store.Missing(service) {
				Respond(w, nil, store.ErrUnavailable)
				return
			}
			fn(w, r)
		}
	}
	mux.HandleFunc("GET /api/store/products", public(func(w http.ResponseWriter, r *http.Request) {
		q, err := supportQuery(r, "offset", "limit", "q", "seller_id", "category_id", "sort", "tag", "stock", "auto_delivery", "ai_processing", "guest_purchase")
		if err != nil {
			Respond(w, nil, err)
			return
		}
		offset, limit, err := Page(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		// These catalogue features need their owned read models. Silently ignoring
		// filters would show the wrong products and misrepresent compatibility.
		for _, key := range []string{"category_id", "sort", "tag", "stock", "auto_delivery", "ai_processing", "guest_purchase"} {
			if q.Get(key) != "" {
				Respond(w, nil, store.ErrUnavailable)
				return
			}
		}
		var seller int64
		if q.Has("seller_id") {
			seller, err = strconv.ParseInt(q.Get("seller_id"), 10, 32)
			if err != nil || seller <= 0 || strconv.FormatInt(seller, 10) != q.Get("seller_id") {
				Respond(w, nil, store.ErrInvalid)
				return
			}
		}
		if len(q.Get("q")) > 200 {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		items, err := service.PublicCatalogue(r.Context(), seller, q.Get("q"), offset, limit)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		Respond(w, map[string]any{"items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit, "seller": nil}, nil)
	}))
	mux.HandleFunc("GET /api/store/products/{id}", public(func(w http.ResponseWriter, r *http.Request) {
		if _, err := supportQuery(r); err != nil {
			Respond(w, nil, err)
			return
		}
		if !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		value, err := service.PublicProduct(r.Context(), r.PathValue("id"))
		Respond(w, value, err)
	}))
	pay := func(w http.ResponseWriter, r *http.Request) {
		self, err := auth.Verify(r.Context(), r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		if store.Missing(service) {
			Respond(w, nil, store.ErrUnavailable)
			return
		}
		if _, err = supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		located, err := service.LocateOrder(r.Context(), r.PathValue("id"))
		if err != nil {
			Respond(w, nil, err)
			return
		}
		if located.Order.Buyer.Kind != "personal" || located.Order.Buyer.ID != self.GetUserId() {
			Respond(w, nil, store.ErrForbidden)
			return
		}
		_, err = service.GetOrder(r.Context(), bearer(r), located.ShopID, located.Order.ID)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		Respond(w, nil, store.ErrPaymentUnavailable)
	}
	mux.HandleFunc("POST /api/store/orders/{id}/pay", pay)
	mux.HandleFunc("POST /api/store/orders/{id}/reconcile", pay)
}

func bearer(r *http.Request) string {
	for key, values := range r.Header {
		if strings.EqualFold(key, "Authorization") && len(values) == 1 {
			return strings.TrimPrefix(values[0], "Bearer ")
		}
	}
	return ""
}
