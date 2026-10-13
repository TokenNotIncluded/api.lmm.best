// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"net/http"
	"strings"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// Mount keeps interactive Web traffic separate from the service-token module
// API while sharing its admission/drain lifecycle. It never upgrades the host
// credential into user authority.
type AdditionalServices struct {
	Assistant  AssistantHistory
	ToolMarket ToolMarketService
	Operations *operations.Service
}

func Mount(host *modules.Host, auth *Authenticator, catalogue StoreService, support SupportService, optional ...AdditionalServices) http.Handler {
	mux := http.NewServeMux()
	RegisterStore(mux, auth, catalogue)
	RegisterSupport(mux, auth, support)
	var operationsService *operations.Service
	if len(optional) == 1 {
		operationsService = optional[0].Operations
		if !store.Missing(optional[0].Assistant) {
			RegisterAssistant(mux, auth, optional[0].Assistant)
		}
		if !store.Missing(optional[0].ToolMarket) {
			RegisterToolMarket(mux, auth, optional[0].ToolMarket)
		}
	}
	RegisterOperations(mux, auth, operationsService)
	// Unassembled old routes are explicit failures, never a successful empty DTO.
	mux.HandleFunc("/api/store/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.Verify(r.Context(), r); err != nil {
			Respond(w, nil, err)
			return
		}
		Respond(w, nil, store.ErrUnavailable)
	})
	return OperationsGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/store/") && !strings.HasPrefix(r.URL.Path, "/api/assistant/") && !strings.HasPrefix(r.URL.Path, "/api/tool-market/") && !strings.HasPrefix(r.URL.Path, "/api/user/auth/store-claim/") &&
			!strings.HasPrefix(r.URL.Path, "/api/release-notes/") && r.URL.Path != "/api/scripts" &&
			!strings.HasPrefix(r.URL.Path, "/api/scripts/") && !strings.HasPrefix(r.URL.Path, "/scripts/") {
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
	}))
}
