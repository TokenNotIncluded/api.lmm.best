// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/operations"
)

// Only a newly created, owned private cluster may supply these fixture files.
// The operations database starts empty; sibling/core databases contain no data.
func TestPostgresOperationsCompositionStorageBoundary(t *testing.T) {
	root := os.Getenv("LMM_OPERATIONS_STORAGE_TEST_DIR")
	if root == "" {
		t.Skip("requires fresh owned operations boundary PostgreSQL fixtures")
	}
	ctx := context.Background()
	storage := StorageConfig{"operations", filepath.Join(root, "operations.url"), "lmm_operations_boundary_test"}
	openFixture := func(name string) *sql.DB {
		t.Helper()
		dsn, err := storageURL(StorageConfig{"operations", filepath.Join(root, name), storage.Database})
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal("private fixture pool unavailable")
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, admin := openFixture("operations-owner.url"), openFixture("operations-admin.url")
	cfg := Config{Selected: []string{"operations"}, Operations: storage,
		Socket:         "/tmp/lmm-operations-boundary-unused.sock",
		HostCredential: []byte("synthetic_operations_host_credential_000001"),
		TokenFile:      paymentPrivate(t, "identity-token", "synthetic_operations_identity_credential_000001")}
	if r, err := New(ctx, cfg); err == nil || r != nil {
		t.Fatal("ordinary startup accepted an uninitialized operations database")
	}
	var installed bool
	if err := owner.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='lmm_operations')").Scan(&installed); err != nil || installed {
		t.Fatalf("ordinary startup performed DDL: installed=%v err=%v", installed, err)
	}
	if err := operations.Initialize(ctx, owner, "lmm_operations_boundary_runtime"); err != nil {
		t.Fatal(err)
	}
	exec := func(query string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, query); err != nil {
			t.Fatal("private operations fixture SQL failed", err)
		}
	}
	noRuntimeConnections := func() {
		t.Helper()
		var active int
		if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename='lmm_operations_boundary_runtime'").Scan(&active); err != nil || active != 0 {
			t.Fatalf("composition leaked runtime pool: connections=%d err=%v", active, err)
		}
	}
	reject := func() {
		t.Helper()
		if r, err := New(ctx, cfg); err == nil || r != nil {
			if r != nil {
				r.Close()
			}
			t.Fatal("unisolated operations composition accepted")
		}
		noRuntimeConnections()
	}
	for _, database := range []string{"lmm_support_boundary_test", "lmm_core_boundary_test"} {
		t.Run("reject CONNECT "+database, func(t *testing.T) {
			exec("GRANT CONNECT ON DATABASE " + database + " TO lmm_operations_boundary_runtime")
			defer exec("REVOKE CONNECT ON DATABASE " + database + " FROM lmm_operations_boundary_runtime")
			reject()
		})
	}
	t.Run("reject role path", func(t *testing.T) {
		exec("CREATE ROLE lmm_operations_hidden NOLOGIN")
		defer exec("DROP ROLE lmm_operations_hidden")
		exec("GRANT lmm_operations_hidden TO lmm_operations_boundary_runtime WITH INHERIT FALSE, SET TRUE")
		defer exec("REVOKE lmm_operations_hidden FROM lmm_operations_boundary_runtime")
		reject()
	})
	for _, grant := range []struct{ table, privilege string }{
		{"contract", "SELECT"}, {"publications", "SELECT"}, {"publications", "INSERT"},
		{"reads", "SELECT"}, {"reads", "INSERT"},
	} {
		t.Run("reject missing "+grant.table+" "+grant.privilege, func(t *testing.T) {
			exec("REVOKE " + grant.privilege + " ON lmm_operations." + grant.table + " FROM lmm_operations_boundary_runtime")
			defer exec("GRANT " + grant.privilege + " ON lmm_operations." + grant.table + " TO lmm_operations_boundary_runtime")
			reject()
		})
	}
	for _, privilege := range []string{"SELECT", "USAGE"} {
		t.Run("reject missing sequence "+privilege, func(t *testing.T) {
			exec("REVOKE " + privilege + " ON SEQUENCE lmm_operations.publication_id FROM lmm_operations_boundary_runtime")
			defer exec("GRANT " + privilege + " ON SEQUENCE lmm_operations.publication_id TO lmm_operations_boundary_runtime")
			reject()
		})
	}
	t.Run("adapter borrows pool on failure", func(t *testing.T) {
		db, err := openStorage(ctx, storage)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if db.Stats().MaxOpenConnections != 2 {
			t.Fatal("operations did not use the bounded composition pool")
		}
		_, err = operations.New(ctx, operations.Config{Releases: db, Scripts: &operations.ScriptConfig{}})
		if !errors.Is(err, operations.ErrUnavailable) {
			t.Fatal("unavailable script snapshot accepted", err)
		}
		if err := db.PingContext(ctx); err != nil {
			t.Fatal("failed construction closed caller-owned pool", err)
		}
	})
	t.Run("composition closes pool after later failure", func(t *testing.T) {
		bad := cfg
		bad.Scripts = &operations.ScriptConfig{}
		if r, err := New(ctx, bad); err == nil || r != nil {
			t.Fatal("script failure accepted")
		}
		noRuntimeConnections()
	})
	r, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstRuntime := r
	t.Cleanup(func() { firstRuntime.Close() })
	if r.Operations == nil || !r.Operations.Availability().ReleaseNotes {
		t.Fatal("operations releases unavailable after guarded composition")
	}
	if _, err := r.Operations.Releases.Publish(ctx, 501, "boundary-v1", "persisted"); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal("idempotent runtime close failed", err)
	}
	noRuntimeConnections()
	if _, err := r.Operations.Releases.Latest(ctx, 101); !errors.Is(err, operations.ErrUnavailable) {
		t.Fatal("closed runtime pool remained usable", err)
	}
	r, err = New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	restartedRuntime := r
	t.Cleanup(func() { restartedRuntime.Close() })
	if note, err := r.Operations.Releases.Latest(ctx, 101); err != nil || note == nil || note.Content != "persisted" {
		t.Fatal("guarded restart lost publication", err)
	}
}
