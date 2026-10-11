// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/support"
)

// SupportService owns authorization against the real conversation/order parties
// and produces the Web DTOs. The transport never invents customer display names.
type SupportService interface {
	RateLimitWeb(context.Context, string, int64, string) error
	OpenWeb(context.Context, string, int64, string, string) (support.WebConversation, error)
	ListWeb(context.Context, string, int64, string, string, bool, int, int) ([]support.WebThread, error)
	HistoryWeb(context.Context, string, int64, string, int64, int) (support.WebHistory, error)
	SendWeb(context.Context, string, int64, string, string, string) (support.WebMessage, error)
	MarkReadWeb(context.Context, string, int64, string, int64) error
	StatusWeb(context.Context, string, int64, string, string) error
	AssistantContextWeb(context.Context, string, int64, string) (map[string]any, error)
	ListCustomersWeb(context.Context, string, int64, string, int, int) ([]support.WebCustomer, error)
	SaveCustomerWeb(context.Context, string, int64, int64, string, []string, int64) error
}

// RegisterSupport preserves the nine authenticated /api/store support routes.
// Authenticator runs for every request; only its verified user ID becomes actor.
func RegisterSupport(mux *http.ServeMux, auth *Authenticator, service SupportService) {
	route := func(pattern string, fn func(http.ResponseWriter, *http.Request, string, int64)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			self, err := auth.Verify(r.Context(), r)
			if err != nil {
				Respond(w, nil, err)
				return
			}
			if store.Missing(service) {
				Respond(w, nil, store.ErrUnavailable)
				return
			}
			var token string
			for name, values := range r.Header {
				if strings.EqualFold(name, "Authorization") {
					token = strings.TrimPrefix(values[0], "Bearer ")
					break
				}
			}
			scope := ""
			switch pattern {
			case "POST /api/store/support/conversations":
				scope = "create"
			case "POST /api/store/support/conversations/{id}/messages":
				scope = "message"
			case "PUT /api/store/support/conversations/{id}/read":
				scope = "receipt"
			case "PUT /api/store/support/conversations/{id}/status":
				scope = "manage"
			}
			if scope != "" {
				if err := service.RateLimitWeb(r.Context(), token, self.GetUserId(), scope); err != nil {
					Respond(w, nil, err)
					return
				}
			}
			fn(w, r, token, self.GetUserId())
		})
	}
	route("GET /api/store/support/conversations", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		q, err := supportQuery(r, "offset", "limit", "role", "status", "unread")
		if err != nil {
			Respond(w, nil, err)
			return
		}
		offset, limit, err := Page(r)
		role, state, unread := "buyer", q.Get("status"), "false"
		if q.Has("role") {
			role = q.Get("role")
		}
		if q.Has("unread") {
			unread = q.Get("unread")
		}
		if err != nil || (role != "buyer" && role != "seller") || (state != "" && state != "open" && state != "resolved") || (unread != "true" && unread != "false") {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		items, err := service.ListWeb(r.Context(), token, actor, role, state, unread == "true", offset, limit)
		RespondList(w, items, offset, limit, err)
	})
	route("POST /api/store/support/conversations", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		if _, err := supportQuery(r); err != nil {
			Respond(w, nil, err)
			return
		}
		var in struct {
			ProductID string `json:"product_id"`
			OrderID   string `json:"order_id"`
		}
		if err := supportDecode(w, r, 4<<10, &in, "product_id", "order_id"); err != nil {
			Respond(w, nil, err)
			return
		}
		if (in.ProductID == "") == (in.OrderID == "") || (in.ProductID != "" && !supportID(in.ProductID)) || (in.OrderID != "" && !supportID(in.OrderID)) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		value, err := service.OpenWeb(r.Context(), token, actor, in.ProductID, in.OrderID)
		Respond(w, value, err)
	})
	route("GET /api/store/support/conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		q, err := supportQuery(r, "before", "limit")
		if err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		before, limit := int64(0), 50
		if q.Has("before") {
			before, err = strconv.ParseInt(q.Get("before"), 10, 64)
		}
		if err != nil {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
		}
		if err != nil || before < 0 || limit < 1 || limit > 100 {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		value, err := service.HistoryWeb(r.Context(), token, actor, r.PathValue("id"), before, limit)
		Respond(w, value, err)
	})
	route("POST /api/store/support/conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		if _, err := supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		var in struct {
			RequestKey string `json:"request_key"`
			Body       string `json:"body"`
		}
		if err := supportDecode(w, r, 32<<10, &in, "request_key", "body"); err != nil {
			Respond(w, nil, err)
			return
		}
		in.Body = strings.TrimSpace(in.Body)
		if len(in.RequestKey) < 8 || len(in.RequestKey) > 64 || strings.ContainsAny(in.RequestKey, "\x00\r\n:") || !utf8.ValidString(in.RequestKey) || in.Body == "" || !supportText(in.Body, 4000) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		value, err := service.SendWeb(r.Context(), token, actor, r.PathValue("id"), in.RequestKey, in.Body)
		Respond(w, value, err)
	})
	route("PUT /api/store/support/conversations/{id}/read", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		if _, err := supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		var in struct {
			ThroughID int64 `json:"through_id"`
		}
		if err := supportDecode(w, r, 1<<10, &in, "through_id"); err != nil {
			Respond(w, nil, err)
			return
		}
		if in.ThroughID <= 0 {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		Respond(w, map[string]any{}, service.MarkReadWeb(r.Context(), token, actor, r.PathValue("id"), in.ThroughID))
	})
	route("PUT /api/store/support/conversations/{id}/status", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		if _, err := supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		var in struct {
			Status string `json:"status"`
		}
		if err := supportDecode(w, r, 1<<10, &in, "status"); err != nil {
			Respond(w, nil, err)
			return
		}
		if in.Status != "open" && in.Status != "resolved" {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		Respond(w, map[string]any{}, service.StatusWeb(r.Context(), token, actor, r.PathValue("id"), in.Status))
	})
	route("GET /api/store/support/conversations/{id}/assistant-context", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		if _, err := supportQuery(r); err != nil || !supportID(r.PathValue("id")) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		value, err := service.AssistantContextWeb(r.Context(), token, actor, r.PathValue("id"))
		Respond(w, value, err)
	})
	route("GET /api/store/my/customers", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		q, err := supportQuery(r, "offset", "limit", "q")
		if err != nil {
			Respond(w, nil, err)
			return
		}
		offset, limit, err := Page(r)
		search := strings.TrimSpace(q.Get("q"))
		if err != nil || !supportText(search, 100) {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		items, err := service.ListCustomersWeb(r.Context(), token, actor, search, offset, limit)
		RespondList(w, items, offset, limit, err)
	})
	route("PUT /api/store/my/customers/{buyer_id}", func(w http.ResponseWriter, r *http.Request, token string, actor int64) {
		if _, err := supportQuery(r); err != nil {
			Respond(w, nil, err)
			return
		}
		buyer, err := strconv.ParseInt(r.PathValue("buyer_id"), 10, 64)
		if err != nil || buyer <= 0 {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		var in struct {
			Note     string   `json:"note"`
			Tags     []string `json:"tags"`
			Revision int64    `json:"revision"`
		}
		if err := supportDecode(w, r, 32<<10, &in, "note", "tags", "revision"); err != nil {
			Respond(w, nil, err)
			return
		}
		in.Note = strings.TrimSpace(in.Note)
		if !supportText(in.Note, 4000) || len(in.Tags) > 10 || in.Revision < 0 || in.Revision >= 9007199254740991 {
			Respond(w, nil, store.ErrInvalid)
			return
		}
		tags := make([]string, 0, len(in.Tags))
		seen := map[string]bool{}
		for _, tag := range in.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" || !supportText(tag, 32) || strings.ContainsAny(tag, "\r\n") {
				Respond(w, nil, store.ErrInvalid)
				return
			}
			if !seen[tag] {
				tags = append(tags, tag)
				seen[tag] = true
			}
		}
		Respond(w, map[string]any{}, service.SaveCustomerWeb(r.Context(), token, actor, buyer, in.Note, tags, in.Revision))
	})
}

func supportQuery(r *http.Request, allowed ...string) (url.Values, error) {
	if r.URL == nil || len(r.URL.RawQuery) > 8192 {
		return nil, store.ErrInvalid
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, store.ErrInvalid
	}
	keys := map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for key, values := range q {
		if !keys[key] || len(values) != 1 {
			return nil, store.ErrInvalid
		}
	}
	return q, nil
}

// Decode an object once, reject duplicate/case-alias fields before ordinary
// decoding, and bound bytes before allocation. No raw decoder error is returned.
func supportDecode(w http.ResponseWriter, r *http.Request, max int64, out any, allowed ...string) error {
	if r.Body == nil {
		return store.ErrInvalid
	}
	if types := r.Header.Values("Content-Type"); len(types) > 1 {
		return store.ErrInvalid
	} else if len(types) == 1 {
		typ, params, err := mime.ParseMediaType(types[0])
		if err != nil || typ != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
			return store.ErrInvalid
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	body, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(body) {
		return store.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return store.ErrInvalid
	}
	keys, seen := map[string]bool{}, map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for d.More() {
		field, err := d.Token()
		key, ok := field.(string)
		if err != nil || !ok || !keys[key] || seen[key] {
			return store.ErrInvalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return store.ErrInvalid
		}
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') {
		return store.ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return store.ErrInvalid
	}
	if err := json.Unmarshal(body, out); err != nil {
		return store.ErrInvalid
	}
	return nil
}

func supportID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func supportText(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, 0)
}
