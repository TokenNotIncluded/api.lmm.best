// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
)

func operationsRequest(mux http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func TestOperationsPublicScriptsAndUnavailable(t *testing.T) {
	root := t.TempDir()
	data := []byte("#!/bin/sh\r\nprintf '<reviewed>'\n")
	hash := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(root, "install.sh"), data, 0600); err != nil {
		t.Fatal(err)
	}
	scripts, err := operations.LoadScripts(operations.ScriptConfig{Root: root, Manifest: []operations.ReviewedScript{{Name: "install.sh", SHA256: hex.EncodeToString(hash[:])}}})
	if err != nil {
		t.Fatal(err)
	}
	core := &authCore{}
	auth := newAuth(t, core)
	mux := http.NewServeMux()
	RegisterOperations(mux, auth, &operations.Service{Scripts: scripts})
	for _, path := range []string{"/api/scripts/install.sh/raw", "/scripts/install.sh"} {
		w := operationsRequest(mux, "GET", path, "", "")
		if w.Code != 200 || w.Body.String() != string(data) || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("%s %d %q", path, w.Code, w.Body.String())
		}
	}
	if core.calls != 0 {
		t.Fatal("public script requires user authority")
	}
	if err := os.WriteFile(filepath.Join(root, "install.sh"), []byte("new unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	w := operationsRequest(mux, "GET", "/scripts/install.sh", "", "")
	if w.Body.String() != string(data) {
		t.Fatal("live filesystem leaked")
	}
	w = operationsRequest(mux, "GET", "/api/scripts", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"install.sh"`) {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/scripts/%2e%2e%2finstall.sh", "/scripts/%2Finstall.sh", "/scripts/a%5Cinstall.sh", "/api/scripts/a.sh%2F/raw", "/scripts/missing.sh", "/scripts/install.sh?url=https://example.invalid"} {
		w = operationsRequest(mux, "GET", path, "", "")
		if w.Code == 200 {
			t.Fatalf("traversal/unknown accepted: %s", path)
		}
	}
	w = operationsRequest(mux, "GET", "/api/release-notes/latest", "", webToken)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal(w.Body.String())
	}
	w = operationsRequest(mux, "GET", "/api/release-notes/latest", "", "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	emptyMux := http.NewServeMux()
	RegisterOperations(emptyMux, auth, nil)
	if w = operationsRequest(emptyMux, "GET", "/api/scripts", "", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	actor := sessionSelf()
	actor.PlatformLevel = 5
	core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) { return actor, nil }
	for _, tc := range [][2]string{{"GET", "/api/scripts/repository"}, {"PUT", "/api/scripts/repository"}, {"POST", "/api/scripts/repository/pull"}} {
		w = operationsRequest(mux, tc[0], tc[1], "", webToken)
		if w.Code != 501 || !strings.Contains(w.Body.String(), "SCRIPT_REPOSITORY_UNSUPPORTED") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if availability := (&operations.Service{Scripts: scripts}).Availability(); availability.ReleaseNotes || !availability.ScriptCatalogue || availability.ScriptRepository {
		t.Fatal(availability)
	}
}
func TestOperationsFreshAdminAuthorizationAndModuleAdmission(t *testing.T) {
	actor := sessionSelf()
	core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) { return actor, nil }}
	auth := newAuth(t, core)
	mux := http.NewServeMux()
	RegisterOperations(mux, auth, nil)
	for level := int32(0); level <= 6; level++ {
		actor.PlatformLevel = level
		for _, tc := range [][3]string{{"GET", "/api/release-notes/admin", ""}, {"POST", "/api/release-notes/admin", `{"version":"v1","content":"x"}`}, {"GET", "/api/scripts/repository", ""}} {
			before := core.calls
			w := operationsRequest(mux, tc[0], tc[1], tc[2], webToken)
			expected := 403
			if level >= 5 {
				expected = 503
				if strings.Contains(tc[1], "repository") {
					expected = 501
				}
			}
			if w.Code != expected || core.calls != before+1 {
				t.Fatalf("level %d %s: %d %s calls=%d", level, tc[1], w.Code, w.Body.String(), core.calls)
			}
		}
	}
	actor.PlatformLevel = 5
	host, err := modules.New([]modules.Module{NewOperationsModule(auth, nil)}, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/extensions/v1/operations/api/release-notes/admin", nil)
	r.Header.Set("Authorization", "Bearer "+hostToken)
	w := httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("host token granted user authority", w.Code)
	}
	r.Header.Set("X-LMM-User-Credential", webToken)
	w = httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	actor.PlatformLevel = 4
	w = httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("fresh demotion ignored", w.Code)
	}
}
func TestOperationsStrictRequestValidation(t *testing.T) {
	actor := sessionSelf()
	actor.PlatformLevel = 5
	core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) { return actor, nil }}
	mux := http.NewServeMux()
	RegisterOperations(mux, newAuth(t, core), nil)
	for _, body := range []string{`{"version":"v1","content":"x","published_by":999}`, `{"version":"v1","version":"v2","content":"x"}`, `{"content":"x"}`, `{"version":5,"content":"x"}`, `{"version":"v1","content":"x"} {}`, `null`, `[]`, `{"version":"v1","content":"x"`, strings.Repeat("x", operations.MaxContent*6+513)} {
		if w := operationsRequest(mux, "POST", "/api/release-notes/admin", body, webToken); w.Code != 422 {
			t.Fatalf("body %q -> %d", body[:min(len(body), 80)], w.Code)
		}
	}
	for _, path := range []string{"/api/release-notes/admin?limit=0", "/api/release-notes/admin?limit=51", "/api/release-notes/admin?limit=", "/api/release-notes/admin?limit=1&limit=2", "/api/release-notes/admin?user_id=1", "/api/release-notes/latest?user_id=1"} {
		if w := operationsRequest(mux, "GET", path, "", webToken); w.Code != 422 {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/api/release-notes/0/read", "/api/release-notes/01/read", "/api/release-notes/9007199254740992/read"} {
		if w := operationsRequest(mux, "POST", path, "", webToken); w.Code != 422 {
			t.Fatal(path, w.Code)
		}
	}
}
func TestOperationsHTTPRealPostgresRestartAndUserIsolation(t *testing.T) {
	dsn := os.Getenv("OPERATIONS_TEST_RUNTIME_DSN")
	if dsn == "" {
		t.Skip("private PostgreSQL runtime DSN not configured")
	}
	service, err := operations.New(context.Background(), operations.Config{Releases: &operations.DatabaseConfig{DSN: dsn}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	actor := sessionSelf()
	actor.UserId = 701
	actor.Owner.Id = 701
	actor.PlatformLevel = 5
	core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) { return actor, nil }}
	mux := http.NewServeMux()
	RegisterOperations(mux, newAuth(t, core), service)
	w := operationsRequest(mux, "POST", "/api/release-notes/admin", `{"version":"http-v1","content":"<reviewed>"}`, webToken)
	var envelope struct {
		Success bool                   `json:"success"`
		Data    operations.ReleaseNote `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || !envelope.Success || envelope.Data.PublishedBy != 701 {
		t.Fatal(w.Code, w.Body.String())
	}
	note := envelope.Data
	actor.UserId = 702
	actor.Owner.Id = 702
	actor.PlatformLevel = 6
	w = operationsRequest(mux, "POST", "/api/release-notes/admin", `{"version":"http-v1","content":"<reviewed>"}`, webToken)
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data != note {
		t.Fatal("replay failed", w.Body.String())
	}
	actor.PlatformLevel = 4
	w = operationsRequest(mux, "POST", "/api/release-notes/admin", `{"version":"http-v1","content":"unauthorized change"}`, webToken)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = operationsRequest(mux, "POST", "/api/release-notes/"+strconv.FormatInt(note.ID, 10)+"/read", "", webToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	actor.UserId = 703
	actor.Owner.Id = 703
	actor.PlatformLevel = 0
	w = operationsRequest(mux, "GET", "/api/release-notes/latest", "", webToken)
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Data != note {
		t.Fatal("cross-user ack or restart persistence failure", w.Body.String())
	}
	for _, user := range []int64{701, 702} {
		actor.UserId = user
		actor.Owner.Id = user
		actor.PlatformLevel = 5
		w = operationsRequest(mux, "GET", "/api/release-notes/admin?limit=50", "", webToken)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"published_by":701`) {
			t.Fatal(w.Body.String())
		}
	}
}
