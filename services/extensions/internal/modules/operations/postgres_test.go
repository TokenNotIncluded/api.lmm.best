// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// Run against a fresh private database. Never point these variables at a site.
func TestPostgresRestrictedRuntimePersistenceConcurrency(t *testing.T) {
	ownerDSN, runtimeDSN := os.Getenv("OPERATIONS_TEST_OWNER_DSN"), os.Getenv("OPERATIONS_TEST_RUNTIME_DSN")
	if ownerDSN == "" || runtimeDSN == "" {
		t.Skip("private PostgreSQL DSNs not configured")
	}
	ctx := context.Background()
	owner, err := sql.Open("postgres", ownerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	raw, err := sql.Open("postgres", runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if Check(ctx, raw) == nil {
		t.Fatal("uninitialized database accepted")
	}
	if err = Initialize(ctx, owner, "operations_runtime"); err != nil {
		t.Fatal(err)
	}
	if err = Initialize(ctx, owner, "operations_runtime"); err == nil {
		t.Fatal("runtime-style repeated DDL accepted")
	}
	if err = Check(ctx, owner); err == nil {
		t.Fatal("owner accepted as runtime")
	}
	s, err := Open(ctx, DatabaseConfig{runtimeDSN})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, e := s.Latest(ctx, 101); e != nil || n != nil {
		t.Fatalf("empty real query %v %v", n, e)
	}
	first, err := s.Publish(ctx, 501, "v1", "first")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Publish(ctx, 601, "v1", "first")
	if err != nil || *duplicate != *first {
		t.Fatalf("replay changed original publication: %v %v", duplicate, err)
	}
	changed, err := s.Publish(ctx, 601, "v1", "changed")
	if err != nil || changed.Revision != 2 {
		t.Fatalf("%v %v", changed, err)
	}
	restored, err := s.Publish(ctx, 501, "v1", "first")
	if err != nil || restored.Revision != 3 || restored.ID == first.ID {
		t.Fatalf("old revision resurrected %v %v", restored, err)
	}
	if err = s.Read(ctx, 101, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Read(ctx, 101, first.ID); err != nil {
		t.Fatal(err)
	}
	if n, e := s.Latest(ctx, 101); e != nil || n.ID != restored.ID {
		t.Fatalf("stale read acknowledged newer publication %v %v", n, e)
	}
	var wg sync.WaitGroup
	results := make(chan *ReleaseNote, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, e := s.Publish(ctx, int64(501+i%2*100), "race", "same")
			if e != nil {
				errs <- e
			} else {
				results <- n
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(results)
	for e := range errs {
		t.Fatal(e)
	}
	var raced *ReleaseNote
	for n := range results {
		if raced == nil {
			raced = n
		} else if *raced != *n {
			t.Fatal("concurrent duplicate diverged")
		}
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if e := s.Read(ctx, 101, raced.ID); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if n, e := s.Latest(ctx, 102); e != nil || n.ID != raced.ID {
		t.Fatalf("cross-user read leak %v %v", n, e)
	}
	list, err := s.List(ctx, 50)
	if err != nil || len(list) != 4 {
		t.Fatalf("%v %v", list, err)
	}
	for _, n := range list {
		if e := s.Read(ctx, 101, n.ID); e != nil {
			t.Fatal(e)
		}
	}
	if n, e := s.Latest(ctx, 101); e != nil || n != nil {
		t.Fatalf("all read %v %v", n, e)
	}
	if err = s.Read(ctx, 101, MaxID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, DatabaseConfig{runtimeDSN})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, e := s.Latest(ctx, 101); e != nil || n != nil {
		t.Fatalf("restart read persistence %v %v", n, e)
	}
	if n, e := s.Latest(ctx, 102); e != nil || n.ID != raced.ID {
		t.Fatalf("restart publication persistence %v %v", n, e)
	}
	// Different concurrent contents append one revision each, without lost writes.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, e := s.Publish(ctx, 501, "distinct-race", fmt.Sprintf("content-%d", i)); e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	concurrent, e := s.List(ctx, 8)
	if e != nil || len(concurrent) != 8 {
		t.Fatal("concurrent distinct history", e)
	}
	for i, n := range concurrent {
		if n.Version != "distinct-race" || n.Revision != 8-i {
			t.Fatal("lost or unordered revision", n)
		}
	}
	for _, statement := range []string{"CREATE TABLE public.bad(id int)", "CREATE TABLE lmm_operations.bad(id int)", "UPDATE lmm_operations.publications SET content='bad'", "DELETE FROM lmm_operations.reads", "TRUNCATE lmm_operations.publications CASCADE", "UPDATE lmm_operations.contract SET fingerprint='bad'", "SELECT setval('lmm_operations.publication_id',1)"} {
		if _, e := raw.ExecContext(ctx, statement); e == nil {
			t.Fatalf("restricted runtime allowed %s", statement)
		}
	}
	if _, err = owner.ExecContext(ctx, "GRANT UPDATE ON lmm_operations.publications TO operations_runtime"); err != nil {
		t.Fatal(err)
	}
	if Check(ctx, raw) == nil {
		t.Fatal("excess runtime grant accepted")
	}
	if _, err = owner.ExecContext(ctx, "REVOKE UPDATE ON lmm_operations.publications FROM operations_runtime"); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ExecContext(ctx, "GRANT UPDATE(content) ON lmm_operations.publications TO operations_runtime"); err != nil {
		t.Fatal(err)
	}
	if Check(ctx, raw) == nil {
		t.Fatal("column privilege escaped allowlist")
	}
	if _, err = owner.ExecContext(ctx, "REVOKE UPDATE(content) ON lmm_operations.publications FROM operations_runtime"); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.ExecContext(ctx, "ALTER TABLE lmm_operations.reads ADD COLUMN drift text"); err != nil {
		t.Fatal(err)
	}
	if Check(ctx, raw) == nil {
		t.Fatal("schema drift accepted")
	}
	if _, err = owner.ExecContext(ctx, "ALTER TABLE lmm_operations.reads DROP COLUMN drift"); err != nil {
		t.Fatal(err)
	}
	// Dropped attributes leave attnum gaps only outside selected visible columns.
	if err = Check(ctx, raw); err != nil {
		t.Fatal("restored schema failed", err)
	}
	if _, err = owner.ExecContext(ctx, "CREATE SCHEMA lmm_core"); err != nil {
		t.Fatal(err)
	}
	if Check(ctx, raw) == nil {
		t.Fatal("shared core schema accepted")
	}
	if _, err = owner.ExecContext(ctx, "DROP SCHEMA lmm_core"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, 501, "capacity", strings.Repeat("x", MaxContent)); err != nil {
		t.Fatal(err)
	}
	// Exercise the durable global cap without thousands of HTTP fixture writes.
	if _, err = owner.ExecContext(ctx, `INSERT INTO lmm_operations.publications(version,revision,content,published_at,published_by) SELECT 'fill-'||g,1,'x',1,501 FROM generate_series(1,10000-(SELECT count(*)::int FROM lmm_operations.publications)) g`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, 501, "over-cap", "x"); err != ErrCapacity {
		t.Fatal("publication cap", err)
	}
	if _, err = s.Publish(ctx, 601, "race", "same"); err != nil {
		t.Fatal("replay at capacity failed", err)
	}
	if _, err = owner.ExecContext(ctx, "DELETE FROM lmm_operations.publications WHERE version LIKE 'fill-%'"); err != nil {
		t.Fatal(err)
	}
}
