// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later.
package toolmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// The fake implements the proposed action boundary. These tests prove module
// behavior, NOT production Rust authorization or cross-process acceptance.
type webTestAuthority struct {
	mu      sync.Mutex
	revoked bool
	fail    error
	calls   int
	hook    func(int)
}

func (a *webTestAuthority) Authorize(_ context.Context, token, account, action string) (Principal, error) {
	a.mu.Lock()
	a.calls++
	calls := a.calls
	hook := a.hook
	a.mu.Unlock()
	if hook != nil {
		hook(calls)
	}
	a.mu.Lock()
	revoked, fail := a.revoked, a.fail
	a.mu.Unlock()
	if fail != nil {
		return Principal{}, fail
	}
	if revoked {
		return Principal{}, ErrAuth
	}
	if token != "1" && token != "2" && token != "3" {
		return Principal{}, ErrAuth
	}
	if account != token {
		return Principal{}, ErrDenied
	}
	if (action == "toolmarket.review" || action == "toolmarket.config") && token != "3" {
		return Principal{}, ErrDenied
	}
	return Principal{User: token, Account: account}, nil
}
func webFixture(t *testing.T) (*Module, *webTestAuthority, *mockMCP) {
	t.Helper()
	mock := newMock(t)
	a := &webTestAuthority{}
	s := &Store{data: emptyDB()}
	m, err := New(s, a, Config{WebOnly: true, CallsPerMinute: 600})
	if err != nil {
		t.Fatal(err)
	}
	useMock(t, m, mock)
	t.Cleanup(func() { _ = s.Close() })
	return m, a, mock
}
func webInput(endpoint, visibility string) WebDraftInput {
	return WebDraftInput{Name: "Synthetic echo", Description: "Fixture only", ExecutionType: "remote", Visibility: visibility, Endpoint: endpoint, AllowedUsers: []int64{}, Tools: []WebToolInput{{Name: "echo", Description: "Custom presentation", InputSchema: json.RawMessage(echoSchema), Permissions: []string{"network"}, PriceQuota: 0}}}
}
func webRequest(t *testing.T, m *Module, user, action, id string, body any) (any, error) {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	return m.LegacyWeb(context.Background(), user, user, WebRequest{Action: action, ID: id, Offset: 0, Limit: 20, Body: raw})
}
func webSaved(t *testing.T, m *Module, user string, in WebDraftInput) WebServiceRecord {
	t.Helper()
	v, err := webRequest(t, m, user, "save", "", in)
	if err != nil {
		t.Fatal(err)
	}
	return v.(WebServiceRecord)
}
func webPublish(t *testing.T, m *Module, user string, s WebServiceRecord) {
	t.Helper()
	if _, err := webRequest(t, m, user, "submit", s.ID, map[string]any{"version_id": s.DraftVersionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := webRequest(t, m, "3", "review", s.ID, map[string]any{"version_id": s.DraftVersionID, "approve": true, "note": "Synthetic remote verified"}); err != nil {
		t.Fatal(err)
	}
}
func webCatalogue(t *testing.T, m *Module, user, text string) []WebSummary {
	t.Helper()
	rows, err := m.PublicWebCatalogue(context.Background(), user, user, WebQuery{Text: text, Offset: 0, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestWebDraftIsOwnedAndUnknownUpdateDoesNotCreate(t *testing.T) {
	m, _, mock := webFixture(t)
	s := webSaved(t, m, "1", webInput(mock.server.URL+"/mcp", "private"))
	for _, action := range []string{"draft", "save", "delete"} {
		var body any
		if action == "save" {
			body = webInput(mock.server.URL+"/mcp", "private")
		}
		if _, err := webRequest(t, m, "2", action, s.ID, body); !errors.Is(err, ErrMissing) {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if _, err := webRequest(t, m, "1", "save", "nonexistent", webInput(mock.server.URL+"/mcp", "public")); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if err := m.store.view(func(d database) error {
		if len(d.Web.Services) != 1 || len(d.Web.Versions) != 1 {
			t.Fatal("unauthorized write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestWebPrivateActivationRequiresExactValidatedFreeVersion(t *testing.T) {
	m, _, mock := webFixture(t)
	in := webInput(mock.server.URL+"/mcp", "private")
	s := webSaved(t, m, "1", in)
	if err := m.webTransition(Principal{"1", "1"}, 1, s.ID, s.DraftVersionID, "activate"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := webRequest(t, m, "1", "activate", s.ID, map[string]any{"version_id": s.DraftVersionID}); err != nil {
		t.Fatal(err)
	}
	if rows := webCatalogue(t, m, "", ""); len(rows) != 0 {
		t.Fatal("private service leaked")
	}
	detail, err := m.PublicWebDetail(context.Background(), "1", "1", s.ID)
	if err != nil || !detail.Validated || detail.Version.Status != "published" || detail.Service.DraftVersionID != "" {
		t.Fatal(detail, err)
	}
	if _, err = m.PublicWebDetail(context.Background(), "2", "2", s.ID); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	install := map[string]any{"client_id": "Web", "tool_id": detail.Tools[0].ToolID, "version_id": detail.Version.ID, "loaded": true}
	if _, err = webRequest(t, m, "2", "install", "", install); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if _, err = webRequest(t, m, "1", "install", "", install); err != nil {
		t.Fatal(err)
	}
	value, err := m.LegacyWeb(context.Background(), "1", "1", WebRequest{Action: "mine", Kind: "installations", Limit: 20})
	if err != nil || len(value.([]WebInstallation)) != 1 {
		t.Fatal(value, err)
	}
	in.Tools[0].PriceQuota = 1
	s2 := webSaved(t, m, "1", in)
	if _, err = webRequest(t, m, "1", "activate", s2.ID, map[string]any{"version_id": s2.DraftVersionID}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}
func TestWebPublicReviewBindsVersionAndPublishedSnapshotsStayImmutable(t *testing.T) {
	m, _, mock := webFixture(t)
	in := webInput(mock.server.URL+"/mcp", "public")
	s := webSaved(t, m, "1", in)
	if _, err := webRequest(t, m, "1", "review", s.ID, map[string]any{"version_id": s.DraftVersionID, "approve": true, "note": "owner"}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	webPublish(t, m, "1", s)
	before, err := m.PublicWebDetail(context.Background(), "", "", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Version.ID != s.DraftVersionID || before.Pricing != "free" || len(before.Tools) != 1 {
		t.Fatal(before)
	}
	if _, err = webRequest(t, m, "2", "install", "", map[string]any{"client_id": "web", "tool_id": before.Tools[0].ToolID, "version_id": before.Version.ID, "loaded": true}); err != nil {
		t.Fatal(err)
	}
	in.Name = "Next unpublished"
	in.Tools[0].PriceQuota = 9007199254740991
	value, err := webRequest(t, m, "1", "save", s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	next := value.(WebServiceRecord)
	current, err := m.PublicWebDetail(context.Background(), "", "", s.ID)
	if err != nil || current.Version.Name != before.Version.Name || current.Tools[0].PriceQuota != 0 || current.Service.DraftVersionID != "" {
		t.Fatal(current, err)
	}
	value, err = webRequest(t, m, "1", "draft", s.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft := value.(WebDetail)
	if draft.Tools[0].ToolID != before.Tools[0].ToolID || draft.Tools[0].PriceQuota != 9007199254740991 {
		t.Fatal(draft)
	}
	webPublish(t, m, "1", next)
	if _, err = webRequest(t, m, "2", "install", "", map[string]any{"client_id": "web", "tool_id": before.Tools[0].ToolID, "version_id": before.Version.ID, "loaded": true}); !errors.Is(err, ErrMissing) {
		t.Fatal("installed stale version", err)
	}
	if err = m.store.view(func(d database) error {
		for _, i := range d.Web.Installations {
			if i.VersionID != before.Version.ID {
				t.Fatal("silently upgraded installation")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestWebSharedVisibilitySearchAndFavoritesReapplyLiveAccess(t *testing.T) {
	m, _, mock := webFixture(t)
	in := webInput(mock.server.URL+"/mcp", "shared")
	in.AllowedUsers = []int64{2}
	in.Tools[0].Description = "Unique 中文 %_ needle"
	s := webSaved(t, m, "1", in)
	webPublish(t, m, "1", s)
	if len(webCatalogue(t, m, "", "needle")) != 0 || len(webCatalogue(t, m, "2", "needle")) != 1 || len(webCatalogue(t, m, "2", "%_")) != 1 || len(webCatalogue(t, m, "2", "%arbitrary")) != 0 {
		t.Fatal("visibility or literal search")
	}
	if _, err := webRequest(t, m, "2", "favorite", s.ID, map[string]bool{"favorite": true}); err != nil {
		t.Fatal(err)
	}
	in.AllowedUsers = []int64{1}
	value, err := webRequest(t, m, "1", "save", s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	webPublish(t, m, "1", value.(WebServiceRecord))
	value, err = m.LegacyWeb(context.Background(), "2", "2", WebRequest{Action: "mine", Kind: "favorites", Limit: 20})
	if err != nil || len(value.([]WebSummary)) != 0 {
		t.Fatal("revoked share leaked favorite", value, err)
	}
}
func TestWebValidationRejectsSchemaDriftAndRevocationBeforeWrite(t *testing.T) {
	m, a, mock := webFixture(t)
	in := webInput(mock.server.URL+"/mcp", "private")
	in.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	s := webSaved(t, m, "1", in)
	if _, err := webRequest(t, m, "1", "validate", s.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	in = webInput(mock.server.URL+"/mcp", "private")
	value, err := webRequest(t, m, "1", "save", s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	s = value.(WebServiceRecord)
	a.mu.Lock()
	next := a.calls + 2
	a.hook = func(call int) {
		if call == next {
			a.mu.Lock()
			a.revoked = true
			a.mu.Unlock()
		}
	}
	a.mu.Unlock()
	if _, err = webRequest(t, m, "1", "validate", s.ID, nil); !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
	if err = m.store.view(func(d database) error {
		if d.Web.Versions[s.DraftVersionID].ValidationDigest != "" {
			t.Fatal("revoked session recorded validation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestWebPendingDraftCannotBeEditedOrWrongVersionApproved(t *testing.T) {
	m, _, mock := webFixture(t)
	in := webInput(mock.server.URL+"/mcp", "public")
	s := webSaved(t, m, "1", in)
	if _, err := webRequest(t, m, "1", "submit", s.ID, map[string]any{"version_id": "wrong"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := webRequest(t, m, "1", "submit", s.ID, map[string]any{"version_id": s.DraftVersionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := webRequest(t, m, "1", "save", s.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := webRequest(t, m, "3", "review", s.ID, map[string]any{"version_id": "wrong", "approve": true, "note": "review"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestWebPauseDeleteAndInstallSeparation(t *testing.T) {
	m, _, mock := webFixture(t)
	s := webSaved(t, m, "1", webInput(mock.server.URL+"/mcp", "public"))
	webPublish(t, m, "1", s)
	detail, err := m.PublicWebDetail(context.Background(), "", "", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = webRequest(t, m, "2", "install", "", map[string]any{"client_id": "web", "tool_id": detail.Tools[0].ToolID, "version_id": detail.Version.ID, "loaded": true}); err != nil {
		t.Fatal(err)
	}
	if _, err = webRequest(t, m, "1", "pause", s.ID, map[string]bool{"paused": true}); err != nil {
		t.Fatal(err)
	}
	if len(webCatalogue(t, m, "", "")) != 0 {
		t.Fatal("paused service visible")
	}
	if _, err = webRequest(t, m, "1", "pause", s.ID, map[string]bool{"paused": false}); err != nil {
		t.Fatal(err)
	}
	if _, err = webRequest(t, m, "1", "delete", s.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err = m.store.view(func(d database) error {
		if len(d.Web.Installations) != 0 || len(d.Executions) != 0 || len(d.Web.Versions) != 1 {
			t.Fatal("delete did not preserve historical version or unload installations")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = webRequest(t, m, "1", "save", s.ID, webInput(mock.server.URL+"/mcp", "public")); !errors.Is(err, ErrMissing) {
		t.Fatal("deleted service resurrected", err)
	}
}
func TestWebConcurrentSavesCreateDistinctVersionSnapshots(t *testing.T) {
	m, _, mock := webFixture(t)
	const writers = 12
	// This fixture exercises snapshot serialization, so admit every planned
	// writer. Production keeps eight slots; the separate admission test checks
	// that exceeding that real bound fails closed.
	m.active = make(chan struct{}, writers)
	in := webInput(mock.server.URL+"/mcp", "private")
	s := webSaved(t, m, "1", in)
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := in
			body.Name = "Concurrent " + strconv.Itoa(i)
			_, err := webRequest(t, m, "1", "save", s.ID, body)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := m.store.view(func(d database) error {
		if len(d.Web.Services) != 1 || len(d.Web.Versions) != writers+1 {
			t.Fatal("lost or overwritten version")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestWebPersistenceEncryptedAndClosedStoreDoesNotReturnEmptySuccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "market.enc")
	key := bytes.Repeat([]byte{19}, 32)
	s, err := InitStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	a := &webTestAuthority{}
	m, err := New(s, a, Config{CallbackURL: "https://app.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	record := webSaved(t, m, "1", webInput("https://example.com/mcp", "private"))
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Contains(raw, []byte("Synthetic echo")) {
		t.Fatal("plaintext file", err)
	}
	s, err = OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	m.store = s
	value, err := webRequest(t, m, "1", "draft", record.ID, nil)
	if err != nil || value.(WebDetail).Version.Name != "Synthetic echo" {
		t.Fatal(value, err)
	}
	s.Close()
	if _, err = m.PublicWebConfig(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = m.PublicWebCatalogue(context.Background(), "", "", WebQuery{Limit: 20}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestWebFundsUnavailableCannotCreateCallsGrantsOrBudgets(t *testing.T) {
	m, _, _ := webFixture(t)
	for _, action := range []string{"invoke", "grant", "budget"} {
		if _, err := webRequest(t, m, "1", action, "", map[string]any{}); !errors.Is(err, ErrFunds) {
			t.Fatal(action, err)
		}
	}
	config, err := m.PublicWebConfig(context.Background())
	if err != nil || config["enabled"] != false || config["credits_per_usd"] != "500000" {
		t.Fatal(config, err)
	}
	if err = m.store.view(func(d database) error {
		if len(d.Executions) != 0 || d.Web != nil {
			t.Fatal("disabled paid action mutated business state")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestWebPublicAndProtectedRequestsShareBoundedModuleAdmission(t *testing.T) {
	m, _, mock := webFixture(t)
	for i := 0; i < cap(m.active); i++ {
		m.active <- struct{}{}
	}
	if _, err := m.PublicWebConfig(context.Background()); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := m.PublicWebCatalogue(context.Background(), "", "", WebQuery{Limit: 20}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := m.PublicWebDetail(context.Background(), "", "", "synthetic"); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := webRequest(t, m, "1", "save", "", webInput(mock.server.URL+"/mcp", "private")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	for i := 0; i < cap(m.active); i++ {
		<-m.active
	}
	if _, err := m.PublicWebConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.PublicWebCatalogue(ctx, "", "", WebQuery{Limit: 20}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestWebOnlyConstructorRequiresNoCallbackAndDisablesNativeEntryPoints(t *testing.T) {
	s := &Store{data: emptyDB()}
	m, err := New(s, &webTestAuthority{}, Config{WebOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/oauth/start", "/mcp", "/meta/execute", "/servers/register"} {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequest("POST", path, bytes.NewBufferString(`{}`)))
		if rec.Code != 503 {
			t.Fatal(path, rec.Code, rec.Body.String())
		}
	}
	if _, err = New(s, &webTestAuthority{}, Config{WebOnly: true, CallbackURL: "https://example.com/callback"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	config, err := m.PublicWebConfig(context.Background())
	if err != nil || config["web_client_id"] != "web-market" {
		t.Fatal(config, err)
	}
}
func TestWebStrictDraftRejectsDuplicateJSONAndUnverifiedPricing(t *testing.T) {
	m, _, mock := webFixture(t)
	in := webInput(mock.server.URL+"/mcp", "public")
	in.Tools[0].ProviderPricing = json.RawMessage(`{"provider":"synthetic","multiplier":"1"}`)
	if _, err := webRequest(t, m, "1", "save", "", in); !errors.Is(err, ErrPrice) {
		t.Fatal(err)
	}
	if _, err := m.LegacyWeb(context.Background(), "1", "1", WebRequest{Action: "save", Body: json.RawMessage(`{"name":"one","name":"two"}`)}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	in = webInput(mock.server.URL+"/mcp", "public")
	in.Tools[0].PriceQuota = 9007199254740992
	if _, err := webRequest(t, m, "1", "save", "", in); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
