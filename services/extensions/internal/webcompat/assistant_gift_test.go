// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAssistantGiftUnavailableNeverInventsBusinessResults(t *testing.T) {
	core := &authCore{}
	port := &assistantPort{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), port)
	for _, op := range []struct{ method, path string }{{"GET", "/api/assistant/new-user-gift"}, {"POST", "/api/assistant/new-user-gift/claim"}} {
		for _, token := range []string{"", hostToken, "lmmk_" + strings.Repeat("k", 43)} {
			response := assistantRequest(mux, op.method, op.path, token, "")
			if response.Code != 401 {
				t.Fatal("invalid user credential", response.Code)
			}
		}
		before := core.calls
		response := assistantRequest(mux, op.method, op.path, webToken, `{"amount":999999,"user_id":123}`)
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != 503 || body["success"] != false || body["code"] != "ASSISTANT_GIFT_UNAVAILABLE" || body["retryable"] != false || core.calls != before+1 {
			t.Fatal(response.Code, body, core.calls)
		}
		if _, ok := body["data"]; ok {
			t.Fatal("unavailable operation fabricated business data")
		}
		if port.calls != 0 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unavailable operation invoked history or became cacheable")
		}
	}
	// Recheck identity even when the business dependency itself is unavailable.
	core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) {
		return nil, status.Error(codes.Unauthenticated, "private revoked credential")
	}
	response := assistantRequest(mux, "POST", "/api/assistant/new-user-gift/claim", webToken, "")
	if response.Code != 401 || strings.Contains(response.Body.String(), "private") {
		t.Fatal(response.Code, response.Body.String())
	}
}
func TestAssistantGiftUnavailabilityDoesNotDependOnHistoryBeingMounted(t *testing.T) {
	core := &authCore{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), nil)
	response := assistantRequest(mux, "GET", "/api/assistant/new-user-gift", webToken, "")
	if response.Code != 503 || !strings.Contains(response.Body.String(), "ASSISTANT_GIFT_UNAVAILABLE") {
		t.Fatal(response.Code, response.Body.String())
	}
}
