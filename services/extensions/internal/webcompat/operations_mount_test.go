// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
)

func TestMountOperationsSharesAdmissionAndGuardsBothEntries(t *testing.T) {
	root := t.TempDir()
	data := []byte("#!/bin/sh\r\necho reviewed\n")
	hash := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(root, "install.sh"), data, 0600); err != nil {
		t.Fatal(err)
	}
	scripts, err := operations.LoadScripts(operations.ScriptConfig{Root: root, Manifest: []operations.ReviewedScript{{Name: "install.sh", SHA256: hex.EncodeToString(hash[:])}}})
	if err != nil {
		t.Fatal(err)
	}
	service := &operations.Service{Scripts: scripts}
	auth := newAuth(t, &authCore{})
	host, err := modules.NewHost([]modules.Module{NewOperationsModule(auth, service)}, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	handler := Mount(host, auth, nil, nil, AdditionalServices{Operations: service})
	for _, path := range []string{"/api/scripts", "/scripts/install.sh", "/api/release-notes/latest"} {
		if w := operationsRequest(handler, "GET", path, "", webToken); w.Code != 503 {
			t.Fatal("preparing accepted", path, w.Code)
		}
	}
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/scripts/install.sh", "/api/scripts/install.sh/raw", "/extensions/v1/operations/scripts/install.sh"} {
		token := ""
		if strings.HasPrefix(path, "/extensions/") {
			token = hostToken
		}
		if w := operationsRequest(handler, "GET", path, "", token); w.Code != 200 || w.Body.String() != string(data) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := operationsRequest(handler, "GET", "/api/release-notes/latest", "", webToken); w.Code != 503 || strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal("missing releases falsely accepted", w.Code, w.Body.String())
	}
	for _, path := range []string{"/scripts//install.sh", "/scripts/%69nstall.sh", "/api/scripts/%2e%2e/raw", "/extensions/v1/operations/scripts//install.sh", "/extensions/v1/operations/scripts/%69nstall.sh"} {
		if w := operationsRequest(handler, "GET", path, "", hostToken); w.Code != 422 || w.Header().Get("Location") != "" {
			t.Fatal("alias redirected", path, w.Code, w.Header())
		}
	}
	host.BeginDrain()
	for _, path := range []string{"/api/scripts", "/scripts/install.sh", "/extensions/v1/operations/scripts/install.sh", "/api/release-notes/latest"} {
		if w := operationsRequest(handler, "GET", path, "", webToken); w.Code != 503 {
			t.Fatal("draining accepted", path, w.Code)
		}
	}
	if w := operationsRequest(handler, http.MethodGet, "/health/live", "", ""); w.Code != 200 {
		t.Fatal("liveness broken", w.Code)
	}
}

func TestMountMissingOperationsIsUnavailableNotHost404(t *testing.T) {
	host, err := modules.NewHost(nil, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	if err := host.MarkReady(); err != nil {
		t.Fatal(err)
	}
	handler := Mount(host, newAuth(t, &authCore{}), nil, nil)
	for _, path := range []string{"/api/scripts", "/api/release-notes/latest"} {
		if w := operationsRequest(handler, "GET", path, "", webToken); w.Code != 503 || strings.Contains(w.Body.String(), `"success":true`) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
