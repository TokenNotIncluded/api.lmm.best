// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Every mutation below is confined to the deliberately named, owned synthetic
// extension database. No core database credential or core data is involved.
func TestPostgresStorageGuards(t *testing.T) {
	root := os.Getenv("LMM_EXTENSION_ASSEMBLY_TEST_DIR")
	if root == "" {
		t.Skip("requires owned synthetic PostgreSQL databases")
	}
	ctx := context.Background()
	cfg := StorageConfig{"store", filepath.Join(root, "store.url"), "lmm_store_assembly_test"}
	dsn, err := storageURL(StorageConfig{"store", filepath.Join(root, "store-admin.url"), cfg.Database})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("open installation fixture")
	}
	defer admin.Close()
	admin.SetMaxOpenConns(1)
	exec := func(query string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, query); err != nil {
			t.Fatal("synthetic isolation fixture SQL failed")
		}
	}
	reject := func() {
		t.Helper()
		db, err := openStorage(ctx, cfg)
		if err == nil {
			db.Close()
			t.Fatal("unisolated runtime pool accepted")
		}
	}
	accept := func() {
		t.Helper()
		db, err := openStorage(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	accept()
	t.Run("foreign schema", func(t *testing.T) {
		exec("CREATE SCHEMA lmm_assistant")
		defer exec("DROP SCHEMA lmm_assistant")
		reject()
	})
	t.Run("core namespace", func(t *testing.T) { exec("CREATE SCHEMA core_probe"); defer exec("DROP SCHEMA core_probe"); reject() })
	t.Run("cross database connect", func(t *testing.T) {
		exec("GRANT CONNECT ON DATABASE lmm_support_assembly_test TO lmm_store_runtime")
		defer exec("REVOKE CONNECT ON DATABASE lmm_support_assembly_test FROM lmm_store_runtime")
		reject()
	})
	t.Run("incomplete runtime grants", func(t *testing.T) {
		exec("REVOKE UPDATE ON lmm_store.products FROM lmm_store_runtime")
		defer exec("GRANT UPDATE ON lmm_store.products TO lmm_store_runtime")
		reject()
	})
	t.Run("hidden SET ROLE path", func(t *testing.T) {
		exec("CREATE ROLE lmm_guard_hidden NOLOGIN")
		defer exec("DROP ROLE lmm_guard_hidden")
		exec("GRANT lmm_guard_hidden TO lmm_store_runtime WITH INHERIT FALSE, SET TRUE")
		defer exec("REVOKE lmm_guard_hidden FROM lmm_store_runtime")
		reject()
	})
	accept()
}
