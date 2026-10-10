package credittransition

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreparationExposesOnlyBoundLocalHealth(t *testing.T) {
	config := testConfig()
	digest := strings.Repeat("c", 64)
	calls := 0
	handler := Handler(config, digest, "0.2.82", func(context.Context) error { calls++; return nil })
	for _, path := range []string{"/api/status", "/api/livez"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000"+path, nil)
		request.RemoteAddr = "127.0.0.1:4096"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response)
		}
		var body struct {
			Success, Ready, Live, Maintenance bool
			BusinessEnabled                   bool `json:"business_enabled"`
			Data                              struct {
				Version string
				Binding Binding `json:"credit_transition"`
			}
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Success || !body.Ready || !body.Maintenance || body.BusinessEnabled || body.Data.Binding != config.Binding(digest) {
			t.Fatalf("unsafe preparation health: %s", response.Body)
		}
		if path == "/api/livez" && !body.Live {
			t.Fatal("missing local liveness")
		}
	}
	if calls != 2 {
		t.Fatal("health did not verify the sealed database")
	}
}

func TestPreparationNeverRunsBusinessOrRemoteHealth(t *testing.T) {
	config := testConfig()
	handler := Handler(config, strings.Repeat("c", 64), "0.2.82", func(context.Context) error { t.Fatal("business request reached database verifier"); return nil })
	for _, test := range []struct {
		method, url, remote string
		forwarded           bool
	}{
		{"POST", "http://127.0.0.1:3000/api/status", "127.0.0.1:1234", false},
		{"GET", "http://127.0.0.1:3000/v1/models", "127.0.0.1:1234", false},
		{"POST", "http://127.0.0.1:3000/api/stripe/webhook", "127.0.0.1:1234", false},
		{"GET", "http://127.0.0.1:3000/", "127.0.0.1:1234", false},
		{"GET", "http://api.lmm.best/api/status", "127.0.0.1:1234", false},
		{"GET", "http://127.0.0.1:3000/api/status", "192.0.2.1:1234", false},
		{"GET", "http://127.0.0.1:3000/api/status", "127.0.0.1:1234", true},
	} {
		request := httptest.NewRequest(test.method, test.url, nil)
		request.RemoteAddr = test.remote
		if test.forwarded {
			request.Header.Set("X-Forwarded-For", "192.0.2.1")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 503 || response.Body.String() != MaintenanceBody(config.TransitionID) {
			t.Fatalf("route admitted: %s %s: %s", test.method, test.url, response.Body)
		}
	}
}

func TestPreparationClosesReadinessAfterFinancialChange(t *testing.T) {
	handler := Handler(testConfig(), strings.Repeat("c", 64), "0.2.82", func(context.Context) error { return errors.New("anchors changed") })
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/api/status", nil)
	request.RemoteAddr = "127.0.0.1:4096"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 503 || !strings.Contains(response.Body.String(), `"success":false`) || strings.Contains(response.Body.String(), "anchors changed") {
		t.Fatal(response.Body)
	}
}
