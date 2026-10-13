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
