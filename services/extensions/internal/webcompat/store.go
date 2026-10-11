// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type StoreService interface {
	PublicCatalogue(context.Context, int64, string, int, int) ([]map[string]any, error)
	PublicProduct(context.Context, string) (map[string]any, error)
	LocateOrder(context.Context, string) (store.LocatedOrder, error)
	GetOrder(context.Context, string, string, string) (store.Order, error)
}

// Optional ports let older hosts retain their supported reads without making a
// missing implementation appear to support the rest of the legacy Web API.
type storeConfigService interface {
	PublicConfig(context.Context) (map[string]any, error)
}
type storeDisclaimerService interface {
	PublicDisclaimer(context.Context, int64) (map[string]any, error)
}
type storeProductTermsService interface {
	PublicProductTerms(context.Context, int64, string) (map[string]any, error)
}
type storeCatalogueService interface {
	PublicCatalogueQuery(context.Context, store.CatalogueQuery) ([]map[string]any, error)
}
type storeLegacyService interface {
	Legacy(context.Context, string, store.Account, store.LegacyRequest) (any, error)
}
type storeWebRoute struct {
	pattern, action string
	bodyLimit       int64
	requiredBody    bool
	page, role      bool
}

// RegisterStore preserves main's paths and integer-credit JSON without moving
// authorization or money decisions into the HTTP layer. Every protected request
// verifies a real Core session before reading a body or calling a domain port.
func RegisterStore(mux *http.ServeMux, auth *Authenticator, service StoreService) {
	public := func(fn func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var actor int64
			for key := range r.Header {
				if strings.EqualFold(key, "Authorization") {
					self, err := auth.Verify(r.Context(), r)
					if err != nil {
						Respond(w, nil, err)
						return
					}
					actor = self.GetUserId()
					break
				}
			}
			if store.Missing(service) {
				Respond(w, nil, store.ErrUnavailable)
				return
			}
			fn(w, r, actor)
		}
	}
	mux.HandleFunc("GET /api/store/config", public(func(w http.ResponseWriter, r *http.Request, _ int64) {
		if _, err := supportQuery(r); err != nil {
			Respond(w, nil, err)
			return
		}
		port, ok := service.(storeConfigService)
		if !ok {
			Respond(w, nil, store.ErrUnavailable)
			return
		}
		value, err := port.PublicConfig(r.Context())
		Respond(w, value, err)
	}))
	mux.HandleFunc("GET /api/store/disclaimer", public(func(w http.ResponseWriter, r *http.Request, actor int64) {
		if _, err := supportQuery(r); err != nil {
			Respond(w, nil, err)
			return
		}
		port, ok := service.(storeDisclaimerService)
		if !ok {
			Respond(w, nil, store.ErrUnavailable)
			return
		}
		value, err := port.PublicDisclaimer(r.Context(), actor)
		Respond(w, value, err)
	}))
	mux.HandleFunc("GET /api/store/products", public(func(w http.ResponseWriter, r *http.Request, _ int64) {
		query, err := storeCatalogueQuery(r)
		if err != nil {
			Respond(w, nil, err)
			return
		}
		var items []map[string]any
		if port, ok := service.(storeCatalogueService); ok {
			items, err = port.PublicCatalogueQuery(r.Context(), query)
		} else {
			if query.Sort != "comprehensive" || query.CategoryID != "" || query.Tag != "" || query.Stock != "" || query.AutoDelivery != nil || query.AIProcessing != nil || query.GuestPurchase != nil {
				Respond(w, nil, store.ErrUnavailable)
				return
			}
			items, err = service.PublicCatalogue(r.Context(), query.Seller, query.Text, query.Offset, query.Limit)
		}
		if err != nil {
			Respond(w, nil, err)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		Respond(w, map[string]any{"items": items, "offset": query.Offset, "limit": query.Limit, "has_more": len(items) == query.Limit, "seller": nil}, nil)
	}))
	mux.HandleFunc("GET /api/store/products/{id}", public(func(w http.ResponseWriter, r *http.Request, _ int64) {
		if _, err := supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		value, err := service.PublicProduct(r.Context(), r.PathValue("id"))
		Respond(w, value, err)
	}))
	mux.HandleFunc("GET /api/store/products/{id}/terms", public(func(w http.ResponseWriter, r *http.Request, actor int64) {
		if _, err := supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		port, ok := service.(storeProductTermsService)
		if !ok {
			Respond(w, nil, store.ErrUnavailable)
			return
		}
		value, err := port.PublicProductTerms(r.Context(), actor, r.PathValue("id"))
		Respond(w, value, err)
	}))

	// Only routes present in main are registered. Pickup proof, inventory and
	// provider-specific refund operations need their own domain ports.
	routes := []storeWebRoute{
		{pattern: "POST /api/store/disclaimer/accept", action: "accept_disclaimer", bodyLimit: 4 << 10, requiredBody: true},
		{pattern: "GET /api/store/my/terms", action: "my_terms"},
		{pattern: "PUT /api/store/my/terms", action: "save_terms", bodyLimit: 128 << 10, requiredBody: true},
		{pattern: "GET /api/store/my/products", action: "my_products", page: true},
		{pattern: "GET /api/store/my/products/{id}", action: "draft_product"},
		{pattern: "GET /api/store/my/products/{id}/preview", action: "preview_product"},
		{pattern: "POST /api/store/products", action: "save_product", bodyLimit: 512 << 10, requiredBody: true},
		{pattern: "PUT /api/store/products/{id}", action: "save_product", bodyLimit: 512 << 10, requiredBody: true},
		{pattern: "POST /api/store/products/{id}/submit", action: "submit_product", bodyLimit: 4 << 10},
		{pattern: "POST /api/store/products/{id}/unlist", action: "unlist_product", bodyLimit: 4 << 10},
		{pattern: "DELETE /api/store/products/{id}", action: "delete_product", bodyLimit: 4 << 10},
		{pattern: "PUT /api/store/products/{id}/paused", action: "paused", bodyLimit: 4 << 10, requiredBody: true},
		{pattern: "PUT /api/store/products/{id}/sale-limit", action: "sale_limit", bodyLimit: 4 << 10, requiredBody: true},
		{pattern: "PUT /api/store/products/{id}/listing", action: "listing", bodyLimit: 4 << 10, requiredBody: true},
		{pattern: "POST /api/store/products/{id}/variants", action: "create_variant", bodyLimit: 512 << 10, requiredBody: true},
		{pattern: "PUT /api/store/products/{id}/variants/{child_id}", action: "save_variant", bodyLimit: 512 << 10, requiredBody: true},
		{pattern: "PUT /api/store/products/{id}/variants/{child_id}/enabled", action: "variant_enabled", bodyLimit: 4 << 10, requiredBody: true},
		{pattern: "GET /api/store/reviews", action: "reviews", page: true},
		{pattern: "POST /api/store/products/{id}/review", action: "review_product", bodyLimit: 8 << 10, requiredBody: true},
		{pattern: "POST /api/store/orders", action: "create_order", bodyLimit: 8 << 10, requiredBody: true},
		{pattern: "GET /api/store/my/orders", action: "my_orders", page: true, role: true},
		{pattern: "GET /api/store/orders/{id}", action: "get_order"},
		{pattern: "POST /api/store/orders/{id}/pay", action: "pay_order", bodyLimit: 4 << 10, requiredBody: true},
		{pattern: "POST /api/store/orders/{id}/reconcile", action: "reconcile_order", bodyLimit: 4 << 10},
		{pattern: "POST /api/store/orders/{id}/cancel", action: "cancel_order", bodyLimit: 4 << 10},
		{pattern: "GET /api/store/orders/{id}/refunds", action: "refunds"},
		{pattern: "POST /api/store/orders/{id}/refunds", action: "request_refund", bodyLimit: 64 << 10, requiredBody: true},
		{pattern: "POST /api/store/orders/{id}/refunds/{child_id}/decision", action: "decide_refund", bodyLimit: 8 << 10, requiredBody: true},
	}
	for _, route := range routes {
		mux.HandleFunc(route.pattern, func(w http.ResponseWriter, r *http.Request) {
			self, err := auth.Verify(r.Context(), r)
			if err != nil {
				Respond(w, nil, err)
				return
			}
			if store.Missing(service) {
				Respond(w, nil, store.ErrUnavailable)
				return
			}
			request, err := storeLegacyQuery(r, route)
			if err != nil {
				Respond(w, nil, err)
				return
			}
			if route.action == "create_order" {
				for key := range r.Header {
					if strings.EqualFold(key, "X-Store-Guest") {
						Respond(w, nil, store.ErrForbidden)
						return
					}
				}
			}
			port, ok := service.(storeLegacyService)
			if !ok {
				if route.action == "pay_order" || route.action == "reconcile_order" {
					storePaymentUnavailable(w, r, service, self.GetUserId())
					return
				}
				Respond(w, nil, store.ErrUnavailable)
				return
			}
			if route.bodyLimit > 0 {
				request.Body, err = storeBody(w, r, route.bodyLimit, route.requiredBody)
				if err != nil {
					Respond(w, nil, err)
					return
				}
			}
			value, err := port.Legacy(r.Context(), bearer(r), store.Account{Kind: "personal", ID: self.GetUserId()}, request)
			Respond(w, value, err)
		})
	}
}

func storeCatalogueQuery(r *http.Request) (store.CatalogueQuery, error) {
	q, err := supportQuery(r, "offset", "limit", "q", "seller_id", "category_id", "sort", "tag", "stock", "auto_delivery", "ai_processing", "guest_purchase")
	if err != nil {
		return store.CatalogueQuery{}, err
	}
	query := store.CatalogueQuery{Text: q.Get("q"), Sort: q.Get("sort"), CategoryID: q.Get("category_id"), Tag: q.Get("tag"), Stock: q.Get("stock")}
	query.Offset, query.Limit, err = Page(r)
	if err != nil {
		return query, err
	}
	if q.Has("seller_id") {
		query.Seller, err = strconv.ParseInt(q.Get("seller_id"), 10, 32)
		if err != nil || query.Seller <= 0 || strconv.FormatInt(query.Seller, 10) != q.Get("seller_id") {
			return query, store.ErrInvalid
		}
	}
	if query.Sort == "" {
		query.Sort = "comprehensive"
	}
	if !utf8.ValidString(query.Text) || len(query.Text) > 200 || !utf8.ValidString(query.Tag) || len(query.Tag) > 512 || (q.Has("category_id") && !storeCategoryID(query.CategoryID)) || (query.Sort != "comprehensive" && query.Sort != "sales" && query.Sort != "newest") || (query.Stock != "" && query.Stock != "in_stock" && query.Stock != "out_of_stock") {
		return query, store.ErrInvalid
	}
	for key, target := range map[string]**bool{"auto_delivery": &query.AutoDelivery, "ai_processing": &query.AIProcessing, "guest_purchase": &query.GuestPurchase} {
		if q.Has(key) {
			value := q.Get(key)
			if value != "true" && value != "false" {
				return query, store.ErrInvalid
			}
			flag := value == "true"
			*target = &flag
		}
	}
	return query, nil
}

// Main accepts canonical lowercase UUID category IDs, including the nil UUID.
func storeCategoryID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func storeLegacyQuery(r *http.Request, route storeWebRoute) (store.LegacyRequest, error) {
	request := store.LegacyRequest{Action: route.action, ID: r.PathValue("id"), ChildID: r.PathValue("child_id")}
	if request.ID != "" && !supportID(request.ID) || request.ChildID != "" && !supportID(request.ChildID) {
		return request, store.ErrInvalid
	}
	var keys []string
	if route.page {
		keys = append(keys, "offset", "limit")
	}
	if route.role {
		keys = append(keys, "role")
	}
	q, err := supportQuery(r, keys...)
	if err != nil {
		return request, err
	}
	if route.page {
		request.Offset, request.Limit, err = Page(r)
		if err != nil {
			return request, err
		}
	}
	if route.role {
		request.Role = "buyer"
		if q.Has("role") {
			request.Role = q.Get("role")
		}
		if request.Role != "buyer" && request.Role != "seller" {
			return request, store.ErrInvalid
		}
	}
	return request, nil
}

// Preserve exact JSON bytes for domain DTO validation and integer credits. The
// transport bounds reads and rejects duplicate fields at every nesting level;
// the domain owns unknown-field and action-specific validation.
func storeBody(w http.ResponseWriter, r *http.Request, max int64, required bool) (json.RawMessage, error) {
	if types := r.Header.Values("Content-Type"); len(types) > 1 {
		return nil, store.ErrInvalid
	} else if len(types) == 1 {
		typ, params, err := mime.ParseMediaType(types[0])
		if err != nil || typ != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
			return nil, store.ErrInvalid
		}
	}
	if r.Body == nil {
		if required {
			return nil, store.ErrInvalid
		}
		return nil, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	body, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(body) {
		return nil, store.ErrInvalid
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 && !required {
		return nil, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, store.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := storeJSONValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, store.ErrInvalid
	}
	return body, nil
}
func storeJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return store.ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return store.ErrInvalid
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			canonical := strings.ToLower(key)
			if err != nil || !ok || seen[canonical] {
				return store.ErrInvalid
			}
			seen[canonical] = true
			if err := storeJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		if last, err := d.Token(); err != nil || last != json.Delim('}') {
			return store.ErrInvalid
		}
	case '[':
		for d.More() {
			if err := storeJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		if last, err := d.Token(); err != nil || last != json.Delim(']') {
			return store.ErrInvalid
		}
	default:
		return store.ErrInvalid
	}
	return nil
}

// Older hosts cannot run a payment action. They still check the actual personal
// buyer and the real order-read authority before reporting the disabled state.
func storePaymentUnavailable(w http.ResponseWriter, r *http.Request, service StoreService, actor int64) {
	located, err := service.LocateOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		Respond(w, nil, err)
		return
	}
	if located.Order.Buyer.Kind != "personal" || located.Order.Buyer.ID != actor {
		Respond(w, nil, store.ErrForbidden)
		return
	}
	if _, err = service.GetOrder(r.Context(), bearer(r), located.ShopID, located.Order.ID); err != nil {
		Respond(w, nil, err)
		return
	}
	Respond(w, nil, store.ErrPaymentUnavailable)
}
func bearer(r *http.Request) string {
	for key, values := range r.Header {
		if strings.EqualFold(key, "Authorization") && len(values) == 1 {
			return strings.TrimPrefix(values[0], "Bearer ")
		}
	}
	return ""
}
