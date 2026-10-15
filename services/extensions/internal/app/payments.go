// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

type PaymentsConfig struct {
	DatabaseName     string `toml:"database_name"`
	DatabaseURLFile  string `toml:"database_url_file"`
	ConfigFile       string `toml:"config_file"`
	CallbacksEnabled bool   `toml:"callbacks_enabled"`
}

func paymentConfigurationKey(key string) bool {
	switch key {
	case "core.payment_token_file", "payments", "payments.database_name", "payments.database_url_file", "payments.config_file", "payments.callbacks_enabled":
		return true
	}
	return false
}
func validatePaymentsConfiguration(cfg GoConfig) error {
	selected := false
	for _, name := range cfg.Host.Modules {
		selected = selected || name == "payments"
	}
	configured := cfg.Core.PaymentTokenFile != "" || cfg.Payments.DatabaseName != "" || cfg.Payments.DatabaseURLFile != "" || cfg.Payments.ConfigFile != "" || cfg.Payments.CallbacksEnabled
	if !selected {
		if configured {
			return errors.New("payments configuration requires explicit module selection")
		}
		return nil
	}
	if cfg.Core.PaymentTokenFile == "" || cfg.Payments.DatabaseName == "" || cfg.Payments.DatabaseURLFile == "" || cfg.Payments.ConfigFile == "" {
		return errors.New("payments requires independent RPC, storage and provider configuration")
	}
	for _, path := range []string{cfg.Core.PaymentTokenFile, cfg.Payments.DatabaseURLFile, cfg.Payments.ConfigFile} {
		if !absolutePath(path) {
			return errors.New("payment configuration paths must be absolute")
		}
	}
	for _, path := range []string{cfg.Core.IdentityTokenFile, cfg.Core.CommerceTokenFile, cfg.Host.TokenFile} {
		if path == cfg.Core.PaymentTokenFile {
			return errors.New("payment RPC credential must be independent")
		}
	}
	return nil
}

type callbackAdmission interface{ Acquire() (func(), bool) }

// Callback-only ingress: the private payment API remains behind host auth.
// No goroutine or per-client state is created. Limits apply across all channels
// and rejected/unknown callbacks never receive a successful acknowledgement.
// @mark extensions.admission role=consumer
func mountPaymentCallbacks(host callbackAdmission, callbacks http.Handler, next http.Handler) http.Handler {
	active := make(chan struct{}, modules.MaxInFlightPerModule)
	var mu sync.Mutex
	window := time.Now()
	count := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/callbacks/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		channel := strings.TrimPrefix(r.URL.Path, "/callbacks/")
		id, err := strconv.ParseInt(channel, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != channel || r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" {
			callbackFailure(w, http.StatusNotFound, "payment_callback_not_found")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			callbackFailure(w, http.StatusMethodNotAllowed, "payment_callback_method")
			return
		}
		signatures := r.Header.Values("Stripe-Signature")
		if len(signatures) != 1 || len(signatures[0]) == 0 || len(signatures[0]) > 2048 {
			callbackFailure(w, http.StatusBadRequest, "invalid_payment_callback")
			return
		}
		for _, b := range []byte(signatures[0]) {
			if b < 32 || b > 126 {
				callbackFailure(w, http.StatusBadRequest, "invalid_payment_callback")
				return
			}
		}
		if r.ContentLength > payments.MaxEvidence {
			callbackFailure(w, http.StatusRequestEntityTooLarge, "payment_callback_too_large")
			return
		}
		var release func()
		var admissionErr error
		if typed, ok := host.(interface{ TryAcquire() (func(), error) }); ok {
			release, admissionErr = typed.TryAcquire()
		} else {
			var ok bool
			release, ok = host.Acquire()
			if !ok {
				admissionErr = modules.ErrNotAccepting
			}
		}
		if admissionErr != nil {
			w.Header().Set("Retry-After", "1")
			label := "extension_not_accepting"
			if errors.Is(admissionErr, modules.ErrSaturated) {
				label = "extension_busy"
			}
			callbackFailure(w, http.StatusServiceUnavailable, label)
			return
		}
		defer release()
		mu.Lock()
		if time.Since(window) >= time.Second {
			window = time.Now()
			count = 0
		}
		limited := count >= 32
		if !limited {
			count++
		}
		mu.Unlock()
		if limited {
			w.Header().Set("Retry-After", "1")
			callbackFailure(w, http.StatusTooManyRequests, "payment_callback_rate_limited")
			return
		}
		select {
		case active <- struct{}{}:
			defer func() { <-active }()
		default:
			w.Header().Set("Retry-After", "1")
			callbackFailure(w, http.StatusServiceUnavailable, "payment_callback_busy")
			return
		}
		bounded, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		controller := http.NewResponseController(w)
		deadline, _ := bounded.Deadline()
		if err := controller.SetReadDeadline(deadline); err != nil {
			callbackFailure(w, http.StatusServiceUnavailable, "payment_callback_ingress_unavailable")
			return
		}
		defer controller.SetReadDeadline(time.Time{})
		r = r.Clone(bounded)
		r.Header.Del("Authorization")
		r.Header.Del("X-LMM-User-Credential")
		r.Header.Del("Cookie")
		r.Body = http.MaxBytesReader(w, r.Body, payments.MaxEvidence)
		callbacks.ServeHTTP(w, r)
	})
}
func callbackFailure(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
