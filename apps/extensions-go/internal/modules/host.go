// Package modules hosts explicitly registered Go modules outside the Rust core.
// It contains no database handle, billing implementation or model relay route.
package modules

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Module is a compile-time Go module, not an arbitrary shared-library loader.
// Handlers receive only their own path below /extensions/v1/<name>/.
// User authorization and a versioned core client must be added to each real
// feature before enabling it. The host's credential is NOT a spending grant.
type Module interface {
	Name() string
	Handler() http.Handler
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func New(registered []Module, credential []byte) (http.Handler, error) {
	if len(credential) < 32 || len(credential) > 4096 {
		return nil, errors.New("extension service credential must contain 32 to 4096 bytes")
	}
	secret := append([]byte(nil), credential...)
	api := http.NewServeMux()
	names := make([]string, 0, len(registered))
	seen := make(map[string]bool, len(registered))
	for _, module := range registered {
		if module == nil {
			return nil, errors.New("nil extension module")
		}
		name := module.Name()
		if !validName.MatchString(name) || name == "modules" || seen[name] {
			return nil, errors.New("invalid, reserved or duplicate extension module name")
		}
		handler := module.Handler()
		if handler == nil {
			return nil, errors.New("extension module has no handler")
		}
		seen[name] = true
		names = append(names, name)
		prefix := "/extensions/v1/" + name
		api.Handle(prefix+"/", http.StripPrefix(prefix, handler))
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
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), secret) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		api.ServeHTTP(w, r)
	}))
	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
