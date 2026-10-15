// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"context"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
)

func TestOperationsConfigurationRequiresExplicitSelection(t *testing.T) {
	for _, cfg := range []Config{
		{Operations: StorageConfig{Component: "operations", Database: "lmm_operations_test"}},
		{Scripts: &operations.ScriptConfig{}},
	} {
		if runtime, err := New(context.Background(), cfg); err == nil || runtime != nil {
			t.Fatal("unselected operations configuration accepted", runtime, err)
		}
	}
}

func TestOperationsScriptsOnlyComposition(t *testing.T) {
	cfg := Config{Selected: []string{"operations"}, Socket: "/tmp/lmm-operations-script-unused.sock",
		HostCredential: []byte("synthetic_operations_host_credential_000001"),
		TokenFile:      paymentPrivate(t, "identity-token", "synthetic_operations_identity_credential_000001"),
		Scripts:        &operations.ScriptConfig{Root: t.TempDir(), Manifest: []operations.ReviewedScript{}}}
	r, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if availability := r.Operations.Availability(); availability.ReleaseNotes || !availability.ScriptCatalogue {
		t.Fatal("scripts-only operations changed", availability)
	}
}
