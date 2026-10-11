// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"net/http"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// Mount keeps interactive Web traffic separate from the service-token module
// API while sharing its admission/drain lifecycle. It never upgrades the host
// credential into user authority.
func Mount(host *modules.Host, auth *Authenticator, catalogue StoreService, support SupportService) http.Handler {
	mux := http.NewServeMux()
	RegisterStore(mux, auth, catalogue)
	RegisterSupport(mux, auth, support)
	// Unassembled old routes are explicit failures, never a successful empty DTO.
	mux.HandleFunc("/api/store/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.Verify(r.Context(), r); err != nil {
			Respond(w, nil, err)
			return
		}
		Respond(w, nil, store.ErrUnavailable)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) < 11 || r.URL.Path[:11] != "/api/store/" {
			host.ServeHTTP(w, r)
			return
		}
		release, ok := host.Acquire()
		if !ok {
			w.Header().Set("Retry-After", "1")
			Respond(w, nil, store.ErrUnavailable)
			return
		}
		defer release()
		mux.ServeHTTP(w, r)
	})
}
