// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package remotecontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"sync"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Core is live Rust authorization, not a host service credential or user-ID hint.
// OAuth sources remain disabled in this native-browser slice.
type Core interface {
	AuthorizeRemote(context.Context, string) (*coreclient.RemoteAuthority, error)
}
type Module struct {
	core        Core
	relay       *relay
	streamsMu   sync.Mutex
	streams     map[int64]int
	streamCount int
	timing      streamTiming
	drain       context.Context
	stop        context.CancelFunc
}

func New(core Core) (*Module, error) {
	if core == nil || (reflect.ValueOf(core).Kind() == reflect.Pointer && reflect.ValueOf(core).IsNil()) {
		return nil, errors.New("remote control requires a core client")
	}
	drain, stop := context.WithCancel(context.Background())
	return &Module{core: core, relay: newRelay(), streams: make(map[int64]int), drain: drain, stop: stop}, nil
}
func (*Module) Name() string { return "remote-control" }

// BeginDrain cancels streams only; ordinary admitted operations finish normally.
func (m *Module) BeginDrain() { m.stop() }

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
var credentialSyntax = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)

const requestLimit = 96 << 10

// Handler is an INTERNAL module route. The extension host must authenticate its
// service credential first. No browser cookie/OAuth token is translated here.
// Registration is explicit; the public Rust adapter only admits native sessions.
// @mark remote-control.commands role=handler
func (m *Module) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pi/sessions/{id}/events", m.events)
	mux.HandleFunc("GET /pi/sessions", func(w http.ResponseWriter, r *http.Request) {
		owner, ok := m.authorize(w, r)
		if !ok {
			return
		}
		success(w, map[string]any{"sessions": m.relay.list(owner)})
	})
	mux.HandleFunc("PUT /pi/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		owner, ok := m.authorize(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !identifier.MatchString(id) {
			failure(w, 400, "invalid_request")
			return
		}
		var input struct {
			DeviceID string     `json:"device_id"`
			Metadata Ciphertext `json:"metadata"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !identifier.MatchString(input.DeviceID) || !validCiphertext(input.Metadata, 16<<10) {
			failure(w, 400, "invalid_ciphertext")
			return
		}
		result, err := m.relay.heartbeat(owner, id, input.DeviceID, input.Metadata)
		if !relayError(w, err) {
			return
		}
		success(w, result)
	})
	mux.HandleFunc("DELETE /pi/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		owner, ok := m.authorize(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !identifier.MatchString(id) {
			failure(w, 400, "invalid_request")
			return
		}
		m.relay.remove(owner, id)
		success(w, map[string]any{})
	})
	mux.HandleFunc("GET /pi/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		owner, ok := m.authorize(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !identifier.MatchString(id) {
			failure(w, 400, "invalid_request")
			return
		}
		query, queryErr := url.ParseQuery(r.URL.RawQuery)
		if queryErr != nil {
			failure(w, 400, "invalid_cursor")
			return
		}
		after := int64(0)
		if values, exists := query["after"]; exists {
			var err error
			if len(values) != 1 {
				failure(w, 400, "invalid_cursor")
				return
			}
			after, err = strconv.ParseInt(values[0], 10, 64)
			if err != nil || after < 0 || after > 9007199254740991 {
				failure(w, 400, "invalid_cursor")
				return
			}
		}
		result, err := m.relay.read(owner, id, after)
		if !relayError(w, err) {
			return
		}
		success(w, map[string]any{"messages": result})
	})
	mux.HandleFunc("POST /pi/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		owner, ok := m.authorize(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !identifier.MatchString(id) {
			failure(w, 400, "invalid_request")
			return
		}
		var input struct {
			Ciphertext
			Sender string `json:"sender"`
		}
		if !decode(w, r, &input) {
			return
		}
		if (input.Sender != "plugin" && input.Sender != "controller") || !validCiphertext(input.Ciphertext, 64<<10) {
			failure(w, 400, "invalid_ciphertext")
			return
		}
		result, err := m.relay.append(owner, id, input.Sender, input.Ciphertext)
		if !relayError(w, err) {
			return
		}
		success(w, result)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
	})
}
func (m *Module) authorize(w http.ResponseWriter, r *http.Request) (int64, bool) {
	actor, denied := m.authorizePrincipal(r.Context(), r)
	if denied != nil {
		failure(w, denied.status, denied.code)
		return 0, false
	}
	return actor.OwnerAccountID, true
}

type authFailure struct {
	status int
	code   string
}

func (m *Module) authorizePrincipal(ctx context.Context, r *http.Request) (*coreclient.RemoteAuthority, *authFailure) {
	credentials := r.Header.Values("X-LMM-User-Credential")
	if len(credentials) != 1 || !credentialSyntax.MatchString(credentials[0]) {
		return nil, &authFailure{401, "unauthorized"}
	}
	actor, err := m.core.AuthorizeRemote(ctx, credentials[0])
	if err != nil {
		code, label := 503, "core_unavailable"
		switch status.Code(err) {
		case codes.Unauthenticated:
			code, label = 401, "unauthorized"
		case codes.PermissionDenied:
			code, label = 403, "forbidden"
		case codes.ResourceExhausted:
			code, label = 429, "core_busy"
		case codes.DeadlineExceeded:
			code, label = 504, "core_timeout"
		case codes.Canceled:
			code, label = 408, "request_canceled"
		}
		return nil, &authFailure{code, label}
	}
	// Native personal browser sessions only; funding accounts are not grants.
	if !actor.Valid() || !actor.Native() {
		return nil, &authFailure{403, "browser_session_required"}
	}
	return actor, nil
}
func validCiphertext(value Ciphertext, max int) bool {
	decode := func(raw string, min, max int) bool {
		if len(raw) > base64.RawURLEncoding.EncodedLen(max) {
			return false
		}
		data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
		return err == nil && len(data) >= min && len(data) <= max && base64.RawURLEncoding.EncodeToString(data) == raw
	}
	return decode(value.Nonce, 12, 12) && decode(value.Ciphertext, 16, max)
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestLimit))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(value)
	if err == nil {
		var extra any
		err = decoder.Decode(&extra)
		if err == io.EOF {
			return true
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		failure(w, 413, "request_too_large")
	} else {
		failure(w, 400, "invalid_request")
	}
	return false
}
func relayError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, ErrNotFound):
		failure(w, 404, "session_unavailable")
	case errors.Is(err, ErrCapacity):
		w.Header().Set("Retry-After", "1")
		failure(w, 429, "relay_capacity")
	case errors.Is(err, ErrConflict):
		failure(w, 409, "session_device_changed")
	default:
		failure(w, 503, "relay_unavailable")
	}
	return false
}
func success(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": value})
}
func failure(w http.ResponseWriter, code int, label string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": label})
}
