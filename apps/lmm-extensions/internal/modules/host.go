// Package modules hosts explicitly registered extensions outside the Rust core.
// A module gets its route, not a core database handle or the host credential.
package modules

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Module is a compiled Go module. This is not a shared-library loader or a
// security sandbox. A module must check user authority through the core client.
// Host authentication never grants permission to spend or manage an account.
type Module interface {
	Name() string
	Handler() http.Handler
}

const MaxInFlightPerModule = 8

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func isNil(value any) bool {
	if value == nil {
		return true
	}
	switch v := reflect.ValueOf(value); v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// New takes a snapshot of registrations. It does not open storage, start workers,
// contact Rust, or run schema installation. Each module has its own admission
// limit. A full module fails immediately rather than creating an unbounded queue.
func New(registered []Module, credential []byte) (http.Handler, error) {
	if len(credential) < 32 || len(credential) > 4096 {
		return nil, errors.New("extension service credential must contain 32 to 4096 bytes")
	}
	secret := append([]byte(nil), credential...)
	api := http.NewServeMux()
	names := make([]string, 0, len(registered))
	seen := make(map[string]bool, len(registered))
	for _, module := range registered {
		if isNil(module) {
			return nil, errors.New("nil extension module")
		}
		name := module.Name()
		if !validName.MatchString(name) || name == "modules" || seen[name] {
			return nil, errors.New("invalid, reserved or duplicate extension module name")
		}
		handler := module.Handler()
		if isNil(handler) {
			return nil, errors.New("extension module has no handler")
		}
		seen[name] = true
		names = append(names, name)
		prefix := "/extensions/v1/" + name
		api.Handle(prefix+"/", http.StripPrefix(prefix, bounded(handler)))
	}
	sort.Strings(names)
	api.HandleFunc("GET /extensions/v1/modules", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			ContractVersion int      `json:"contract_version"`
			Modules         []string `json:"modules"`
		}{1, names})
	})
	mux := http.NewServeMux()
	for _, path := range []string{"/health/live", "/health/ready"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "alive", "service": "lmm-extensions"})
		})
	}
	mux.Handle("/extensions/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		if len(values) != 1 {
			unauthorized(w)
			return
		}
		token, ok := strings.CutPrefix(values[0], "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), secret) != 1 {
			unauthorized(w)
			return
		}
		// Clone before removing the host credential. Keep the separate user
		// credential; the selected module still has to validate it with Rust.
		r = r.Clone(r.Context())
		r.Header.Del("Authorization")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		api.ServeHTTP(w, r)
	}))
	return mux, nil
}

func bounded(next http.Handler) http.Handler {
	active := make(chan struct{}, MaxInFlightPerModule)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case active <- struct{}{}:
			defer func() { <-active }()
		default:
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "module_busy"})
			return
		}
		// Let net/http close a response when a handler panics. Do not append a
		// successful JSON document to a partially written or streaming response.
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
