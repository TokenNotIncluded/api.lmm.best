// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/legal"
)

func TestListenerGuardsScriptsBeforeOutermostSharedMux(t *testing.T) {
	policies, err := legal.Load("")
	if err != nil {
		t.Fatal(err)
	}
	forwarded := 0
	handler := listenerHandler(policies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded++
		_, _ = w.Write([]byte("exact snapshot\r\n"))
	}))
	for _, path := range []string{"/scripts/install.sh", "/api/scripts/install.sh/raw", "/extensions/v1/operations/scripts/install.sh", "/health/live"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.String() != "exact snapshot\r\n" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	before := forwarded
	for _, path := range []string{"/scripts//install.sh", "/scripts/../scripts/install.sh", "/scripts/%69nstall.sh", "/scripts/%2E%2E/install.sh", "/api/scripts/%69nstall.sh/raw", "/extensions/v1/operations/scripts//install.sh", "/extensions/v1/operations/scripts/%69nstall.sh", "/extensions/v1/operations/scripts/%2e%2e/install.sh"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 422 || w.Header().Get("Location") != "" || forwarded != before {
			t.Fatal("unguarded redirect/dispatch", path, w.Code, w.Header(), forwarded)
		}
	}
}
