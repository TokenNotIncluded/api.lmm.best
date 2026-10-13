// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

type assistantPort struct {
	token string
	owner int64
	calls int
	click string
}

func (p *assistantPort) Status(_ context.Context, token string) (map[string]any, error) {
	p.calls++
	p.token = token
	return map[string]any{"enabled": false, "route_available": false}, nil
}
func (p *assistantPort) List(_ context.Context, token string, owner int64, _ bool, _ int, _ string) (assistant.HistoryPage, error) {
	p.calls++
	p.token, p.owner = token, owner
	return assistant.HistoryPage{Conversations: []assistant.Conversation{{ID: 42, Title: "Stored conversation", Owner: "self"}}}, nil
}
func (p *assistantPort) Detail(context.Context, string, int64, int) (assistant.HistoryDetail, error) {
	p.calls++
	return assistant.HistoryDetail{}, nil
}
func (p *assistantPort) Archive(context.Context, string, int64, bool) (assistant.ArchiveResult, error) {
	p.calls++
	return assistant.ArchiveResult{}, nil
}
func (p *assistantPort) PresetClick(_ context.Context, id string) error {
	p.calls++
	p.click = id
	return nil
}

type assistantGreetingPort struct {
	assistantPort
	language string
	template string
	revision int64
	err      error
}

func (p *assistantGreetingPort) Greeting(_ context.Context, token string) (assistant.GreetingPreference, error) {
	p.calls++
	p.token = token
	return assistant.GreetingPreference{Templates: map[string]string{}, Revision: 0, UpdatedAt: 0}, p.err
}
func (p *assistantGreetingPort) UpdateGreeting(_ context.Context, token, language, template string, revision int64) (assistant.GreetingPreference, error) {
	p.calls++
	p.token, p.language, p.template, p.revision = token, language, template, revision
	return assistant.GreetingPreference{Templates: map[string]string{language: template}, Revision: revision + 1, UpdatedAt: 123}, p.err
}

func assistantRequest(mux http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func TestAssistantWebEnvelopeAndCurrentBearerArePreserved(t *testing.T) {
	core, port := &authCore{}, &assistantPort{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), port)
	w := assistantRequest(mux, "GET", "/api/assistant/conversations?user_id=17", webToken, "")
	var envelope struct {
		Success bool                  `json:"success"`
		Data    assistant.HistoryPage `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil {
		t.Fatal(w.Body.String())
	}
	if w.Code != 200 || !envelope.Success || len(envelope.Data.Conversations) != 1 || port.token != webToken || port.owner != 17 || core.calls != 1 {
		t.Fatal(w.Code, w.Body.String(), port)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
}
func TestAssistantWebRejectsAmbiguousInputsAndNeverFakesChat(t *testing.T) {
	core, port := &authCore{}, &assistantPort{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), port)
	for _, path := range []string{"/api/assistant/conversations?limit=1&limit=2", "/api/assistant/conversations?archived=wrong", "/api/assistant/conversations?user_id=0", "/api/assistant/conversations?limit=101", "/api/assistant/conversations?bad=%ZZ"} {
		w := assistantRequest(mux, "GET", path, webToken, "")
		if w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if port.calls != 0 {
		t.Fatal("invalid query reached domain")
	}
	w := assistantRequest(mux, "POST", "/api/assistant/chat", webToken, `{"message":"hello"}`)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if port.calls != 0 {
		t.Fatal("unconfigured chat invoked model or stored history")
	}
}
func TestAssistantWebRequiresSessionForReadsAndKeepsPresetsPublic(t *testing.T) {
	core, port := &authCore{}, &assistantPort{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), port)
	for _, token := range []string{"", hostToken, "lmmk_" + strings.Repeat("k", 43)} {
		w := assistantRequest(mux, "GET", "/api/assistant/status", token, "")
		if w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := assistantRequest(mux, "GET", "/api/assistant/pre-conversation-presets?language=zh-TW", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "存取權限") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = assistantRequest(mux, "POST", "/api/assistant/pre-conversation-presets/getting_started/click", "", "")
	if w.Code != 200 || port.click != "getting_started" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAssistantGreetingWebContract(t *testing.T) {
	core, port := &authCore{}, &assistantGreetingPort{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), port)
	w := assistantRequest(mux, "GET", "/api/assistant/workspace/greeting", webToken, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":{"templates":{},"revision":0,"updated_at":0}`) || port.token != webToken {
		t.Fatal(w.Code, w.Body.String(), port)
	}
	w = assistantRequest(mux, "PUT", "/api/assistant/workspace/greeting", webToken, `{"language":"zh-TW","template":"HI,$name; $$","revision":2}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revision":3`) || port.language != "zh-TW" || port.template != "HI,$name; $$" || port.revision != 2 || port.token != webToken {
		t.Fatal(w.Code, w.Body.String(), port)
	}
	port.err = access.ErrConflict
	w = assistantRequest(mux, "PUT", "/api/assistant/workspace/greeting", webToken, `{"language":"en","template":"","revision":2}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"success":false`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAssistantGreetingWebRejectsAmbiguousBodiesAndOwnerInjection(t *testing.T) {
	core, port := &authCore{}, &assistantGreetingPort{}
	mux := http.NewServeMux()
	RegisterAssistant(mux, newAuth(t, core), port)
	for _, body := range []string{
		`{}`, `null`, `[]`,
		`{"language":"en","revision":0}`,
		`{"language":"en","template":null,"revision":0}`,
		`{"language":null,"template":"","revision":0}`,
		`{"language":"en","template":"","revision":null}`,
		`{"language":"en","template":"","revision":0.5}`,
		`{"language":"en","template":"","revision":0,"user_id":17}`,
		`{"language":"en","template":"","revision":0,"revision":1}`,
		`{"language":"en","template":"","revision":0} {}`,
		`{"language":"en","template":"` + strings.Repeat("a", 16<<10) + `","revision":0}`,
	} {
		w := assistantRequest(mux, "PUT", "/api/assistant/workspace/greeting", webToken, body)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		for _, query := range []string{"?user_id=17", "?language=en&language=zh", "?bad=%ZZ"} {
			w := assistantRequest(mux, method, "/api/assistant/workspace/greeting"+query, webToken, `{"language":"en","template":"","revision":0}`)
			if w.Code != 400 {
				t.Fatal(method, query, w.Code)
			}
		}
		for _, token := range []string{"", hostToken, "lmmk_" + strings.Repeat("k", 43)} {
			w := assistantRequest(mux, method, "/api/assistant/workspace/greeting", token, `{"language":"en","template":"","revision":0}`)
			if w.Code != 401 {
				t.Fatal(method, w.Code)
			}
		}
	}
	if port.calls != 0 {
		t.Fatal("invalid request reached greeting domain", port.calls)
	}
}

func TestAssistantGreetingWebMissingDependencyIsUnavailableNotEmptySuccess(t *testing.T) {
	for _, port := range []AssistantHistory{nil, &assistantPort{}} {
		mux := http.NewServeMux()
		RegisterAssistant(mux, newAuth(t, &authCore{}), port)
		for _, method := range []string{"GET", "PUT"} {
			w := assistantRequest(mux, method, "/api/assistant/workspace/greeting", webToken, `{"language":"en","template":"","revision":0}`)
			if w.Code != 503 || !strings.Contains(w.Body.String(), `"success":false`) {
				t.Fatal(method, w.Code, w.Body.String())
			}
		}
	}
}
