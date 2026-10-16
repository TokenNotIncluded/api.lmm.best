// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package forum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errTooLarge = errors.New("request_too_large")
var errMediaType = errors.New("unsupported_media_type")

func (*Service) Name() string { return "forum" }

// @mark forum.http role=implementation
// Handler is module-relative, mounted by the existing host at
// /extensions/v1/forum/. It does NOT authenticate the host credential itself.
// Browser/webcompat mounting is a separate, not-yet-implemented adapter.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	type endpoint func(context.Context, string, http.ResponseWriter, *http.Request) (any, error)
	register := func(pattern string, status int, paged bool, fn endpoint) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			tokens := r.Header.Values("X-LMM-User-Credential")
			if len(r.Header.Values("Authorization")) != 0 || len(tokens) != 1 || !identifier(tokens[0], 32, 256) {
				respond(w, nil, ErrUnauthorized, 0)
				return
			}
			if !paged && r.URL.RawQuery != "" {
				respond(w, nil, ErrInvalid, 0)
				return
			}
			value, err := fn(r.Context(), tokens[0], w, r)
			respond(w, value, err, status)
		})
	}
	register("GET /boards", 200, true, func(ctx context.Context, token string, _ http.ResponseWriter, r *http.Request) (any, error) {
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Boards(ctx, token, p)
	})
	register("POST /boards", 201, false, func(ctx context.Context, token string, w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := decode(w, r, &in); err != nil {
			return nil, err
		}
		key, err := creationKey(r)
		if err != nil {
			return nil, err
		}
		return s.AddBoard(ctx, token, key, in.Name)
	})
	register("GET /boards/{board}/topics", 200, true, func(ctx context.Context, token string, _ http.ResponseWriter, r *http.Request) (any, error) {
		id, err := pathID(r, "board")
		if err != nil {
			return nil, err
		}
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Topics(ctx, token, id, p)
	})
	register("POST /boards/{board}/topics", 201, false, func(ctx context.Context, token string, w http.ResponseWriter, r *http.Request) (any, error) {
		id, err := pathID(r, "board")
		if err != nil {
			return nil, err
		}
		var in struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err = decode(w, r, &in); err != nil {
			return nil, err
		}
		key, err := creationKey(r)
		if err != nil {
			return nil, err
		}
		return s.AddTopic(ctx, token, key, id, in.Title, in.Body)
	})
	register("GET /topics/{topic}", 200, false, func(ctx context.Context, token string, _ http.ResponseWriter, r *http.Request) (any, error) {
		id, err := pathID(r, "topic")
		if err != nil {
			return nil, err
		}
		return s.Topic(ctx, token, id)
	})
	register("GET /topics/{topic}/replies", 200, true, func(ctx context.Context, token string, _ http.ResponseWriter, r *http.Request) (any, error) {
		id, err := pathID(r, "topic")
		if err != nil {
			return nil, err
		}
		p, err := pagination(r)
		if err != nil {
			return nil, err
		}
		return s.Replies(ctx, token, id, p)
	})
	register("POST /topics/{topic}/replies", 201, false, func(ctx context.Context, token string, w http.ResponseWriter, r *http.Request) (any, error) {
		id, err := pathID(r, "topic")
		if err != nil {
			return nil, err
		}
		var in struct {
			Body string `json:"body"`
		}
		if err = decode(w, r, &in); err != nil {
			return nil, err
		}
		key, err := creationKey(r)
		if err != nil {
			return nil, err
		}
		return s.AddReply(ctx, token, key, id, in.Body)
	})
	for _, route := range []struct {
		pattern, variable string
		action            Action
	}{
		{"POST /topics/{topic}/lock", "topic", LockTopic},
		{"POST /topics/{topic}/pin", "topic", PinTopic},
		{"POST /topics/{topic}/hide", "topic", HideTopic},
		{"POST /replies/{reply}/hide", "reply", HideReply},
	} {
		register(route.pattern, 200, false, func(ctx context.Context, token string, w http.ResponseWriter, r *http.Request) (any, error) {
			id, err := pathID(r, route.variable)
			if err != nil {
				return nil, err
			}
			var version int64
			var reason string
			value := true
			if route.action == HideTopic || route.action == HideReply {
				var in struct {
					ExpectedVersion int64  `json:"expected_version"`
					Reason          string `json:"reason"`
				}
				if err = decode(w, r, &in); err != nil {
					return nil, err
				}
				version, reason = in.ExpectedVersion, in.Reason
			} else {
				var in struct {
					ExpectedVersion int64  `json:"expected_version"`
					Value           *bool  `json:"value"`
					Reason          string `json:"reason"`
				}
				if err = decode(w, r, &in); err != nil {
					return nil, err
				}
				if in.Value == nil {
					return nil, ErrInvalid
				}
				version, reason, value = in.ExpectedVersion, in.Reason, *in.Value
			}
			return s.Moderate(ctx, token, route.action, id, version, value, reason)
		})
	}
	return mux
}
func respond(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalid):
			status, err = 400, ErrInvalid
		case errors.Is(err, ErrUnauthorized):
			status, err = 401, ErrUnauthorized
		case errors.Is(err, ErrForbidden):
			status, err = 403, ErrForbidden
		case errors.Is(err, ErrNotFound):
			status, err = 404, ErrNotFound
		case errors.Is(err, ErrConflict):
			status, err = 409, ErrConflict
		case errors.Is(err, ErrLocked):
			status, err = 409, ErrLocked
		case errors.Is(err, errTooLarge):
			status = 413
		case errors.Is(err, errMediaType):
			status = 415
		case errors.Is(err, ErrUnknownCommit):
			status, err = 503, ErrUnknownCommit
		default:
			status, err = 503, ErrUnavailable
		}
		value = struct {
			Error string `json:"error"`
		}{err.Error()}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func creationKey(r *http.Request) (string, error) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !identifier(keys[0], 1, 128) {
		return "", ErrInvalid
	}
	return keys[0], nil
}
func decimal(s string, positive bool) (int64, error) {
	if s == "" || len(s) > 19 {
		return 0, ErrInvalid
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 0 || (positive && id == 0) {
		return 0, ErrInvalid
	}
	return id, nil
}
func pathID(r *http.Request, key string) (int64, error) { return decimal(r.PathValue(key), true) }
func pagination(r *http.Request) (PageRequest, error) {
	p := PageRequest{Limit: DefaultPageSize}
	// Query() silently drops malformed values; ParseQuery must succeed instead.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return p, ErrInvalid
	}
	for k, v := range values {
		if len(v) != 1 {
			return p, ErrInvalid
		}
		n, e := decimal(v[0], false)
		if e != nil {
			return p, e
		}
		switch k {
		case "after_id":
			p.AfterID = n
		case "limit":
			if n > MaxPageSize {
				return p, ErrInvalid
			}
			p.Limit = int(n)
		default:
			return p, ErrInvalid
		}
	}
	if !p.Valid() {
		return p, ErrInvalid
	}
	return p, nil
}

// All payloads are flat objects. Validate exact keys, scalar values, UTF-8 and
// surrogate escapes BEFORE Go's permissive JSON field matching/replacement.
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	headers := r.Header.Values("Content-Type")
	if len(headers) != 1 {
		return errMediaType
	}
	kind, params, err := mime.ParseMediaType(headers[0])
	if err != nil || kind != "application/json" {
		return errMediaType
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return errMediaType
		}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errTooLarge
		}
		return ErrInvalid
	}
	if !utf8.Valid(data) || !validSurrogates(data) {
		return ErrInvalid
	}
	allowed := map[string]struct{}{}
	typ := reflect.TypeOf(out).Elem()
	for i := 0; i < typ.NumField(); i++ {
		allowed[typ.Field(i).Tag.Get("json")] = struct{}{}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]struct{}{}
	for dec.More() {
		k, e := dec.Token()
		if e != nil {
			return ErrInvalid
		}
		key, ok := k.(string)
		if !ok {
			return ErrInvalid
		}
		if _, ok = allowed[key]; !ok {
			return ErrInvalid
		}
		if _, ok = seen[key]; ok {
			return ErrInvalid
		}
		seen[key] = struct{}{}
		v, e := dec.Token()
		if e != nil || v == nil {
			return ErrInvalid
		}
		if _, nested := v.(json.Delim); nested {
			return ErrInvalid
		}
	}
	if _, err = dec.Token(); err != nil {
		return ErrInvalid
	}
	if _, err = dec.Token(); err != io.EOF {
		return ErrInvalid
	}
	if err = json.Unmarshal(data, out); err != nil {
		return ErrInvalid
	}
	return nil
}
func validSurrogates(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		n, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
