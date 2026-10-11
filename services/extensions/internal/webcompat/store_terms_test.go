// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package webcompat

import (
	"net/http/httptest"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

func TestStoreTransportActualConfigAdvertisesTermsWithoutAccess(t *testing.T) {
	repo, authority, funds := &storeReadOnlyRepoFake{}, &storeAuthorityFake{}, &storeFundsNeverFake{}
	service, err := store.New(repo, authority, funds)
	if err != nil {
		t.Fatal(err)
	}
	core := &authCore{}
	rec := httptest.NewRecorder()
	storeTransportMux(t, core, service).ServeHTTP(rec, storeTransportRequest("GET", "/api/store/config", nil))
	data := supportEnvelope(t, rec)["data"].(map[string]any)
	if rec.Code != 200 || data["store_terms_supported"] != true || data["store_access_supported"] != false || data["fixed_content_supported"] != false {
		t.Fatalf("terms capability implied unsupported access or delivery: status=%d data=%v", rec.Code, data)
	}
	if core.calls != 0 || len(authority.checks) != 0 || repo.readCalls != 0 || repo.writeCalls != 0 || funds.availableCalls != 0 || funds.executeCalls != 0 {
		t.Fatal("public capability read consulted identity, storage or funds")
	}
}
