// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// RegisterOperations installs only the content routes. The parent must supply
// admission/drain, proxy routing and the existing live Core Authenticator, and
// wrap the actual outer admission handler with OperationsGuard BEFORE any mux.
// A registered handler cannot prevent ServeMux's pre-dispatch path redirects.
func RegisterOperations(mux *http.ServeMux, auth *Authenticator, s *operations.Service) {
	protected := func(admin bool, fn func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			actor, err := auth.Verify(r.Context(), r)
			if err != nil {
				operationsRespond(w, nil, err)
				return
			}
			if actor.GetUserId() > operations.MaxID {
				operationsRespond(w, nil, store.ErrForbidden)
				return
			}
			if admin && actor.GetPlatformLevel() != 5 && actor.GetPlatformLevel() != 6 {
				operationsRespond(w, nil, store.ErrForbidden)
				return
			}
			fn(w, r, actor.GetUserId())
		}
	}
	releases := func(w http.ResponseWriter) bool {
		if s == nil || s.Releases == nil {
			operationsRespond(w, nil, operations.ErrUnavailable)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/release-notes/latest", protected(false, func(w http.ResponseWriter, r *http.Request, user int64) {
		if !operationsQuery(r, false) {
			operationsRespond(w, nil, operations.ErrInvalid)
			return
		}
		if !releases(w) {
			return
		}
		n, err := s.Releases.Latest(r.Context(), user)
		operationsRespond(w, n, err)
	}))
	mux.HandleFunc("POST /api/release-notes/{id}/read", protected(false, func(w http.ResponseWriter, r *http.Request, user int64) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 || id > operations.MaxID || strconv.FormatInt(id, 10) != r.PathValue("id") || !operationsQuery(r, false) || !operationsEmptyBody(w, r) {
			operationsRespond(w, nil, operations.ErrInvalid)
			return
		}
		if !releases(w) {
			return
		}
		operationsRespond(w, nil, s.Releases.Read(r.Context(), user, id))
	}))
	mux.HandleFunc("GET /api/release-notes/admin", protected(true, func(w http.ResponseWriter, r *http.Request, _ int64) {
		if !operationsQuery(r, true) {
			operationsRespond(w, nil, operations.ErrInvalid)
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 50 {
				operationsRespond(w, nil, operations.ErrInvalid)
				return
			}
		}
		if !releases(w) {
			return
		}
		n, err := s.Releases.List(r.Context(), limit)
		operationsRespond(w, n, err)
	}))
	mux.HandleFunc("POST /api/release-notes/admin", protected(true, func(w http.ResponseWriter, r *http.Request, user int64) {
		if !operationsQuery(r, false) {
			operationsRespond(w, nil, operations.ErrInvalid)
			return
		}
		version, content, err := operationsPublication(w, r)
		if err != nil {
			operationsRespond(w, nil, err)
			return
		}
		if !releases(w) {
			return
		}
		n, err := s.Releases.Publish(r.Context(), user, version, content)
		operationsRespond(w, n, err)
	}))
	public := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Also reject decoded aliases when these handlers are registered on
			// a mux directly. Pre-dispatch redirects require the outer guard.
			if !operationsCanonicalScriptURL(r) {
				operationsRespond(w, nil, operations.ErrInvalid)
				return
			}
			for key := range r.Header {
				if strings.EqualFold(key, "Authorization") {
					if _, err := auth.Verify(r.Context(), r); err != nil {
						operationsRespond(w, nil, err)
						return
					}
					break
				}
			}
			if !operationsQuery(r, false) {
				operationsRespond(w, nil, operations.ErrInvalid)
				return
			}
			fn(w, r)
		}
	}
	mux.HandleFunc("GET /api/scripts", public(func(w http.ResponseWriter, _ *http.Request) {
		if s == nil {
			operationsRespond(w, nil, operations.ErrUnavailable)
			return
		}
		v, err := s.Scripts.List()
		operationsRespond(w, v, err)
	}))
	raw := public(func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			operationsRespond(w, nil, operations.ErrUnavailable)
			return
		}
		data, err := s.Scripts.Raw(r.PathValue("name"))
		if err != nil {
			operationsRespond(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/scripts/{name}/raw", raw)
	mux.HandleFunc("GET /scripts/{name}", raw)
	unsupported := protected(true, func(w http.ResponseWriter, _ *http.Request, _ int64) {
		operationsRespond(w, nil, operations.ErrUnsupported)
	})
	mux.HandleFunc("GET /api/scripts/repository", unsupported)
	mux.HandleFunc("PUT /api/scripts/repository", unsupported)
	mux.HandleFunc("POST /api/scripts/repository/pull", unsupported)
}

// NewOperationsModule can be registered with modules.New. Its relative routes
// are /api/release-notes/*, /api/scripts/* and /scripts/* under the module prefix.
// Host credentials grant admission only; X-LMM-User-Credential is verified by Core.
// The module guards its own mux. Parent must also wrap the outer shared host with
// OperationsGuard so that shared ServeMux redirects cannot precede rejection.
func NewOperationsModule(auth *Authenticator, s *operations.Service) modules.Module {
	mux := http.NewServeMux()
	RegisterOperations(mux, auth, s)
	return &operationsModule{OperationsGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		values := r.Header.Values("X-LMM-User-Credential")
		r.Header.Del("Authorization")
		if len(values) > 0 {
			r.Header["Authorization"] = make([]string, len(values))
			for i, v := range values {
				r.Header["Authorization"][i] = "Bearer " + v
			}
		}
		mux.ServeHTTP(w, r)
	}))}
}

// OperationsGuard must wrap the parent's outer Web/extension admission handler,
// before any shared ServeMux. Only script namespaces (including the operations
// module prefix and paths which normalize into those namespaces) are guarded.
// Canonical script basenames need no URL escapes. Reject aliases and traversal
// rather than redirecting them to a reviewed file; leave unrelated routes alone.
func OperationsGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !operationsCanonicalScriptURL(r) {
			operationsRespond(w, nil, operations.ErrInvalid)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func operationsScriptPath(p string) (string, bool) {
	const prefix = "/extensions/v1/operations"
	if strings.HasPrefix(p, prefix+"/") {
		p = strings.TrimPrefix(p, prefix)
	}
	return p, p == "/api/scripts" || strings.HasPrefix(p, "/api/scripts/") || p == "/scripts" || strings.HasPrefix(p, "/scripts/")
}
func operationsCanonicalScriptURL(r *http.Request) bool {
	p, relevant := operationsScriptPath(r.URL.Path)
	_, cleanRelevant := operationsScriptPath(path.Clean(r.URL.Path))
	if !relevant && !cleanRelevant {
		return true
	}
	if !relevant || r.URL.EscapedPath() != r.URL.Path || path.Clean(r.URL.Path) != r.URL.Path {
		return false
	}
	switch p {
	case "/api/scripts", "/api/scripts/repository", "/api/scripts/repository/pull":
		return true
	}
	if name, ok := strings.CutPrefix(p, "/scripts/"); ok {
		return operations.ValidScriptName(name)
	}
	if name, ok := strings.CutPrefix(p, "/api/scripts/"); ok {
		name, ok = strings.CutSuffix(name, "/raw")
		return ok && operations.ValidScriptName(name)
	}
	return false
}

type operationsModule struct{ handler http.Handler }

func (*operationsModule) Name() string            { return "operations" }
func (m *operationsModule) Handler() http.Handler { return m.handler }

func operationsQuery(r *http.Request, limit bool) bool {
	if len(r.URL.RawQuery) > 128 {
		return false
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range q {
		if !limit || key != "limit" || len(values) != 1 || values[0] == "" {
			return false
		}
	}
	return true
}
func operationsEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	return err == nil && len(b) == 0
}
func operationsPublication(w http.ResponseWriter, r *http.Request) (string, string, error) {
	if contentType := r.Header.Get("Content-Type"); contentType != "application/json" && contentType != "application/json; charset=utf-8" {
		return "", "", operations.ErrInvalid
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, operations.MaxContent*6+512))
	if err != nil || !utf8.Valid(raw) {
		return "", "", operations.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", operations.ErrInvalid
	}
	fields := map[string]string{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return "", "", operations.ErrInvalid
		}
		key, ok := token.(string)
		if !ok || (key != "version" && key != "content") {
			return "", "", operations.ErrInvalid
		}
		if _, exists := fields[key]; exists {
			return "", "", operations.ErrInvalid
		}
		var value string
		if d.Decode(&value) != nil {
			return "", "", operations.ErrInvalid
		}
		fields[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || len(fields) != 2 {
		return "", "", operations.ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return "", "", operations.ErrInvalid
	}
	return fields["version"], fields["content"], nil
}
func operationsRespond(w http.ResponseWriter, value any, err error) {
	code := http.StatusOK
	body := map[string]any{"success": true, "data": value}
	if err != nil {
		label := "OPERATIONS_UNAVAILABLE"
		message := "Operational content is unavailable."
		code = http.StatusServiceUnavailable
		switch {
		case errors.Is(err, store.ErrUnauthorized):
			code = 401
			label = "OPERATIONS_LOGIN_REQUIRED"
			message = "Sign in to access this content."
		case errors.Is(err, store.ErrForbidden):
			code = 403
			label = "OPERATIONS_ACCESS_DENIED"
			message = "This operation is not available to this account."
		case errors.Is(err, operations.ErrInvalid):
			code = 422
			label = "OPERATIONS_INVALID_INPUT"
			message = "Invalid operational content request."
		case errors.Is(err, operations.ErrNotFound):
			code = 404
			label = "OPERATIONS_NOT_FOUND"
			message = "Content was not found."
		case errors.Is(err, operations.ErrCapacity):
			code = 409
			label = "OPERATIONS_CAPACITY"
			message = "Publication capacity has been reached."
		case errors.Is(err, operations.ErrUnsupported):
			code = 501
			label = "SCRIPT_REPOSITORY_UNSUPPORTED"
			message = "Script repository configuration and pull are not supported."
		}
		body = map[string]any{"success": false, "code": label, "message": message}
	}
	encoded, e := json.Marshal(body)
	// Includes worst-case JSON escaping for the bounded 50-publication history.
	if e != nil || len(encoded) > 10<<20 {
		code = 503
		encoded = []byte(`{"success":false,"code":"OPERATIONS_UNAVAILABLE","message":"Operational content is unavailable."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.Write(append(encoded, '\n'))
}
