// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type AssistantHistory interface {
	Status(context.Context, string) (map[string]any, error)
	List(context.Context, string, int64, bool, int, string) (assistant.HistoryPage, error)
	Detail(context.Context, string, int64, int) (assistant.HistoryDetail, error)
	Archive(context.Context, string, int64, bool) (assistant.ArchiveResult, error)
	PresetClick(context.Context, string) error
}

type AssistantGreeting interface {
	Greeting(context.Context, string) (assistant.GreetingPreference, error)
	UpdateGreeting(context.Context, string, string, string, int64) (assistant.GreetingPreference, error)
}

// RegisterAssistant preserves the old public presets, personal greeting and
// interactive history routes. A shared host must wrap these routes in its own
// admission/drain gate.
// No browser may inject assistant messages or select a payer/model/owner.
func RegisterAssistant(mux *http.ServeMux, auth *Authenticator, history AssistantHistory) {
	protected := func(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, err := auth.Verify(r.Context(), r); err != nil {
				assistantRespond(w, nil, assistantAuthError(err))
				return
			}
			if access.Missing(history) {
				assistantRespond(w, nil, access.ErrUnavailable)
				return
			}
			token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			fn(w, r, token)
		}
	}
	public := func(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			for name := range r.Header {
				if strings.EqualFold(name, "Authorization") {
					if _, err := auth.Verify(r.Context(), r); err != nil {
						assistantRespond(w, nil, assistantAuthError(err))
						return
					}
					break
				}
			}
			if access.Missing(history) {
				assistantRespond(w, nil, access.ErrUnavailable)
				return
			}
			fn(w, r)
		}
	}
	mux.HandleFunc("GET /api/assistant/pre-conversation-presets", public(func(w http.ResponseWriter, r *http.Request) {
		query, err := assistantQuery(r)
		if err != nil {
			assistantRespond(w, nil, err)
			return
		}
		assistantRespond(w, assistant.HistoryPresets(query.Get("language")), nil)
	}))
	mux.HandleFunc("POST /api/assistant/pre-conversation-presets/{id}/click", public(func(w http.ResponseWriter, r *http.Request) {
		assistantRespond(w, nil, history.PresetClick(r.Context(), r.PathValue("id")))
	}))
	greeting, greetingAvailable := history.(AssistantGreeting)
	mux.HandleFunc("GET /api/assistant/workspace/greeting", protected(func(w http.ResponseWriter, r *http.Request, token string) {
		if !greetingAvailable || access.Missing(greeting) {
			assistantRespond(w, nil, access.ErrUnavailable)
			return
		}
		query, err := assistantQuery(r)
		if err != nil || len(query) != 0 {
			assistantRespond(w, nil, access.ErrInvalid)
			return
		}
		value, err := greeting.Greeting(r.Context(), token)
		assistantRespond(w, value, err)
	}))
	mux.HandleFunc("PUT /api/assistant/workspace/greeting", protected(func(w http.ResponseWriter, r *http.Request, token string) {
		if !greetingAvailable || access.Missing(greeting) {
			assistantRespond(w, nil, access.ErrUnavailable)
			return
		}
		query, err := assistantQuery(r)
		if err != nil || len(query) != 0 {
			assistantRespond(w, nil, access.ErrInvalid)
			return
		}
		var body struct {
			Language *string `json:"language"`
			Template *string `json:"template"`
			Revision *int64  `json:"revision"`
		}
		if err = access.Body(w, r, &body); err != nil || body.Language == nil || body.Template == nil || body.Revision == nil {
			assistantRespond(w, nil, access.ErrInvalid)
			return
		}
		value, err := greeting.UpdateGreeting(r.Context(), token, *body.Language, *body.Template, *body.Revision)
		assistantRespond(w, value, err)
	}))
	mux.HandleFunc("GET /api/assistant/status", protected(func(w http.ResponseWriter, r *http.Request, token string) {
		value, err := history.Status(r.Context(), token)
		assistantRespond(w, value, err)
	}))
	mux.HandleFunc("GET /api/assistant/conversations", protected(func(w http.ResponseWriter, r *http.Request, token string) {
		query, err := assistantQuery(r)
		if err != nil {
			assistantRespond(w, nil, err)
			return
		}
		limit, err := assistantLimit(query, 30)
		var owner int64
		var archived bool
		if raw, ok := query["user_id"]; ok && err == nil {
			owner, err = strconv.ParseInt(raw[0], 10, 64)
			if owner <= 0 {
				err = access.ErrInvalid
			}
		}
		if raw, ok := query["archived"]; ok && err == nil {
			archived, err = strconv.ParseBool(raw[0])
		}
		if err != nil {
			assistantRespond(w, nil, access.ErrInvalid)
			return
		}
		value, err := history.List(r.Context(), token, owner, archived, limit, query.Get("cursor"))
		assistantRespond(w, value, err)
	}))
	mux.HandleFunc("GET /api/assistant/conversations/{id}", protected(func(w http.ResponseWriter, r *http.Request, token string) {
		id, err := assistantConversationID(r)
		if err != nil {
			assistantRespond(w, nil, err)
			return
		}
		query, err := assistantQuery(r)
		if err != nil {
			assistantRespond(w, nil, err)
			return
		}
		limit, err := assistantLimit(query, 100)
		if err != nil {
			assistantRespond(w, nil, err)
			return
		}
		value, err := history.Detail(r.Context(), token, id, limit)
		assistantRespond(w, value, err)
	}))
	for _, operation := range []string{"archive", "unarchive"} {
		archived := operation == "archive"
		mux.HandleFunc("POST /api/assistant/conversations/{id}/"+operation, protected(func(w http.ResponseWriter, r *http.Request, token string) {
			id, err := assistantConversationID(r)
			if err != nil {
				assistantRespond(w, nil, err)
				return
			}
			var body struct{}
			if err = access.Body(w, r, &body); err != nil {
				assistantRespond(w, nil, err)
				return
			}
			value, err := history.Archive(r.Context(), token, id, archived)
			assistantRespond(w, value, err)
		}))
	}
	// No Rust gift grant/receipt contract is installed. An authenticated user
	// gets an explicit unavailable outcome, never an invented offer or credit.
	giftUnavailable := func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.Verify(r.Context(), r); err != nil {
			assistantRespond(w, nil, assistantAuthError(err))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "code": "ASSISTANT_GIFT_UNAVAILABLE", "retryable": false,
			"message": "New-user gifts are not available on this installation.",
		})
	}
	mux.HandleFunc("GET /api/assistant/new-user-gift", giftUnavailable)
	mux.HandleFunc("POST /api/assistant/new-user-gift/claim", giftUnavailable)
	mux.HandleFunc("POST /api/assistant/chat", protected(func(w http.ResponseWriter, r *http.Request, _ string) {
		// No Model.Next adapter is installed: SESSION -> ApiKey substitution
		// would change the payer and bypass the missing sponsorship policy.
		assistantRespond(w, nil, access.ErrUnavailable)
	}))
}

func assistantConversationID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || id > 9007199254740991 {
		return 0, access.ErrNotFound
	}
	return id, nil
}
func assistantQuery(r *http.Request) (url.Values, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 2048 {
		return nil, access.ErrInvalid
	}
	for _, values := range query {
		if len(values) != 1 {
			return nil, access.ErrInvalid
		}
	}
	return query, nil
}
func assistantLimit(query url.Values, fallback int) (int, error) {
	if raw, ok := query["limit"]; ok {
		limit, err := strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 {
			return 0, access.ErrInvalid
		}
		return limit, nil
	}
	return fallback, nil
}
func assistantAuthError(err error) error {
	for _, pair := range [][2]error{{store.ErrUnauthorized, access.ErrUnauthorized}, {store.ErrForbidden, access.ErrForbidden}, {store.ErrInvalid, access.ErrInvalid}} {
		if errors.Is(err, pair[0]) {
			return pair[1]
		}
	}
	return access.ErrUnavailable
}

func assistantRespond(w http.ResponseWriter, value any, err error) {
	code := http.StatusOK
	body := map[string]any{"success": true, "message": "", "data": value}
	if err != nil {
		code = http.StatusServiceUnavailable
		label := "ASSISTANT_UNAVAILABLE"
		for _, pair := range []struct {
			err   error
			code  int
			label string
		}{{access.ErrInvalid, 400, "ASSISTANT_INVALID_REQUEST"}, {access.ErrUnauthorized, 401, "ASSISTANT_SESSION_REQUIRED"},
			{access.ErrForbidden, 403, "ASSISTANT_ACCESS_DENIED"}, {access.ErrNotFound, 404, "ASSISTANT_HISTORY_NOT_FOUND"},
			{access.ErrConflict, 409, "ASSISTANT_HISTORY_CONFLICT"}, {access.ErrLimit, 429, "ASSISTANT_LIMIT_REACHED"}} {
			if errors.Is(err, pair.err) {
				code, label = pair.code, pair.label
				break
			}
		}
		body = map[string]any{"success": false, "code": label, "message": "Assistant request could not be completed."}
	}
	data, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		code = http.StatusServiceUnavailable
		data = []byte(`{"success":false,"code":"ASSISTANT_UNAVAILABLE","message":"Assistant request could not be completed."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
}
