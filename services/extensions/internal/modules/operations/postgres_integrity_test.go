// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

func fixtureSQL(t *testing.T, ctx context.Context, db *sql.DB, statement string, args ...any) {
	t.Helper()
	t.Logf("private fixture SQL: %s; args=%v", statement, args)
	if _, err := db.ExecContext(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
}
func rejectInstalledDatabase(t *testing.T, ctx context.Context, runtime *sql.DB, dsn string) {
	t.Helper()
	if err := Check(ctx, runtime); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("drift accepted by Check: %v", err)
	}
	s, err := Open(ctx, DatabaseConfig{DSN: dsn})
	if s != nil {
		s.Close()
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("drift accepted by Open: %v", err)
	}
}
func testPostgresInitializeIsolation(t *testing.T, ctx context.Context, owner, admin *sql.DB) {
	t.Helper()
	t.Run("InitializeRejectsLiteralPgLookalikeApplicationSchema", func(t *testing.T) {
		fixtureSQL(t, ctx, owner, "CREATE SCHEMA pgx_core")
		defer fixtureSQL(t, ctx, owner, "DROP SCHEMA pgx_core CASCADE")
		fixtureSQL(t, ctx, owner, "CREATE TABLE pgx_core.records(id bigint)")
		if err := Initialize(ctx, owner, "operations_runtime"); err == nil {
			t.Fatal("initialized alongside pgx_core even without runtime CREATE")
		}
		fixtureSQL(t, ctx, owner, "GRANT CREATE ON SCHEMA pgx_core TO operations_runtime")
		if err := Initialize(ctx, owner, "operations_runtime"); err == nil {
			t.Fatal("initialized alongside pgx_core application storage")
		}
	})
	// A fixture superuser is needed to deliberately add CREATE on real system
	// schemas. Neither installation-owner nor runtime credentials are elevated.
	for _, name := range []string{"public", "information_schema", "pg_catalog", "pg_toast"} {
		t.Run("InitializeRejectsCreate_"+name, func(t *testing.T) {
			fixtureSQL(t, ctx, admin, "GRANT CREATE ON SCHEMA "+pq.QuoteIdentifier(name)+" TO operations_runtime")
			defer fixtureSQL(t, ctx, admin, "REVOKE CREATE ON SCHEMA "+pq.QuoteIdentifier(name)+" FROM operations_runtime")
			if err := Initialize(ctx, owner, "operations_runtime"); err == nil {
				t.Fatalf("initialized with CREATE on %s", name)
			}
			var installed bool
			if err := owner.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='lmm_operations')").Scan(&installed); err != nil || installed {
				t.Fatalf("failed installation left DDL: %v %v", installed, err)
			}
		})
	}
}
func testPostgresInstalledIntegrity(t *testing.T, ctx context.Context, owner, admin, runtime *sql.DB, dsn string, s *Releases) {
	t.Helper()
	t.Run("OrdinaryRestrictedInstallationStillAccepted", func(t *testing.T) {
		if err := Check(ctx, runtime); err != nil {
			t.Fatal(err)
		}
		opened, err := Open(ctx, DatabaseConfig{DSN: dsn})
		if err != nil {
			t.Fatal(err)
		}
		opened.Close()
	})
	t.Run("CheckRejectsLiteralPgLookalikeApplicationSchema", func(t *testing.T) {
		fixtureSQL(t, ctx, owner, "CREATE SCHEMA pgx_core")
		defer fixtureSQL(t, ctx, owner, "DROP SCHEMA pgx_core CASCADE")
		fixtureSQL(t, ctx, owner, "CREATE TABLE pgx_core.records(id bigint)")
		rejectInstalledDatabase(t, ctx, runtime, dsn) // isolation without runtime DDL authority
		fixtureSQL(t, ctx, owner, "GRANT CREATE ON SCHEMA pgx_core TO operations_runtime")
		rejectInstalledDatabase(t, ctx, runtime, dsn)
	})
	for _, name := range []string{"public", "lmm_operations", "information_schema", "pg_catalog", "pg_toast"} {
		t.Run("CheckRejectsCreate_"+name, func(t *testing.T) {
			fixtureSQL(t, ctx, admin, "GRANT CREATE ON SCHEMA "+pq.QuoteIdentifier(name)+" TO operations_runtime")
			defer fixtureSQL(t, ctx, admin, "REVOKE CREATE ON SCHEMA "+pq.QuoteIdentifier(name)+" FROM operations_runtime")
			rejectInstalledDatabase(t, ctx, runtime, dsn)
		})
	}
	t.Run("UnreadPublicationRLSCannotReopenAsSuccessfulNull", func(t *testing.T) {
		unread, err := s.Latest(ctx, 901)
		if err != nil || unread == nil {
			t.Fatalf("expected persisted unread note before drift: %v %v", unread, err)
		}
		fixtureSQL(t, ctx, owner, "ALTER TABLE lmm_operations.publications ENABLE ROW LEVEL SECURITY")
		defer fixtureSQL(t, ctx, owner, "ALTER TABLE lmm_operations.publications DISABLE ROW LEVEL SECURITY")
		var visible int
		if err = runtime.QueryRowContext(ctx, "SELECT count(*) FROM lmm_operations.publications").Scan(&visible); err != nil || visible != 0 {
			t.Fatalf("RLS counterexample not reproduced: %d %v", visible, err)
		}
		rejectInstalledDatabase(t, ctx, runtime, dsn)
	})
	t.Run("DisabledRLSWithPolicyStillRejected", func(t *testing.T) {
		fixtureSQL(t, ctx, owner, "CREATE POLICY drift ON lmm_operations.publications FOR SELECT USING (true)")
		defer fixtureSQL(t, ctx, owner, "DROP POLICY drift ON lmm_operations.publications")
		rejectInstalledDatabase(t, ctx, runtime, dsn)
	})
	t.Run("ForceRLSFlagStillRejected", func(t *testing.T) {
		fixtureSQL(t, ctx, owner, "ALTER TABLE lmm_operations.publications FORCE ROW LEVEL SECURITY")
		defer fixtureSQL(t, ctx, owner, "ALTER TABLE lmm_operations.publications NO FORCE ROW LEVEL SECURITY")
		rejectInstalledDatabase(t, ctx, runtime, dsn)
	})
	t.Run("RelationOwnerFingerprintDriftRejected", func(t *testing.T) {
		fixtureSQL(t, ctx, admin, "ALTER TABLE lmm_operations.publications OWNER TO operations_observer")
		defer fixtureSQL(t, ctx, admin, "ALTER TABLE lmm_operations.publications OWNER TO operations_owner")
		rejectInstalledDatabase(t, ctx, runtime, dsn)
	})
	t.Run("BenignThirdRoleACLDriftRejected", func(t *testing.T) {
		fixtureSQL(t, ctx, owner, "GRANT SELECT ON lmm_operations.publications TO operations_observer")
		defer fixtureSQL(t, ctx, owner, "REVOKE SELECT ON lmm_operations.publications FROM operations_observer")
		rejectInstalledDatabase(t, ctx, runtime, dsn)
	})
	if err := Check(ctx, runtime); err != nil {
		t.Fatal("restored ordinary installation rejected", err)
	}
	t.Run("EpochSecondsSchemaAndScanBounds", func(t *testing.T) {
		for _, seconds := range []int64{-1, 0, MaxPublishedAt + 1, MaxID + 2} {
			var id int64
			err := owner.QueryRowContext(ctx, "INSERT INTO lmm_operations.publications(version,revision,content,published_at,published_by) VALUES ('invalid-time',1,'x',$1,501) RETURNING id", seconds).Scan(&id)
			if err == nil {
				t.Fatalf("schema accepted invalid epoch seconds %d", seconds)
			}
		}
		var highID int64
		for i, seconds := range []int64{1, MaxPublishedAt} {
			n, err := scanNote(owner.QueryRowContext(ctx, "INSERT INTO lmm_operations.publications(version,revision,content,published_at,published_by) VALUES ('epoch-boundary',$1,'time-boundary',$2,501) RETURNING "+noteColumns, i+1, seconds))
			if err != nil || n.PublishedAt != seconds {
				t.Fatalf("boundary rejected/changed: %v %v", n, err)
			}
			highID = n.ID
			encoded, err := json.Marshal(n)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				PublishedAt float64 `json:"published_at"`
			}
			if err = json.Unmarshal(encoded, &decoded); err != nil || int64(decoded.PublishedAt) != seconds || int64(decoded.PublishedAt*1000) != seconds*1000 {
				t.Fatalf("rounded JSON/date milliseconds: %d %v", seconds, err)
			}
		}
		// Bypass only this private fixture constraint to prove scanning rejects
		// out-of-contract persisted rows, even on an already-open runtime pool.
		fixtureSQL(t, ctx, owner, "ALTER TABLE lmm_operations.publications DROP CONSTRAINT publications_published_at_check")
		defer func() {
			fixtureSQL(t, ctx, owner, "UPDATE lmm_operations.publications SET published_at=$1 WHERE id=$2", MaxPublishedAt, highID)
			fixtureSQL(t, ctx, owner, fmt.Sprintf("ALTER TABLE lmm_operations.publications ADD CONSTRAINT publications_published_at_check CHECK (published_at BETWEEN 1 AND %d)", MaxPublishedAt))
		}()
		for _, seconds := range []int64{-1, 0, MaxPublishedAt + 1, MaxID + 2} {
			fixtureSQL(t, ctx, owner, "UPDATE lmm_operations.publications SET published_at=$1 WHERE id=$2", seconds, highID)
			rejectInstalledDatabase(t, ctx, runtime, dsn)
			if n, err := s.Latest(ctx, 901); !errors.Is(err, ErrUnavailable) || n != nil {
				t.Fatalf("invalid timestamp escaped latest: %v %v", n, err)
			}
			if list, err := s.List(ctx, 50); !errors.Is(err, ErrUnavailable) || list != nil {
				t.Fatalf("invalid timestamp escaped history: %v %v", list, err)
			}
			if n, err := s.Publish(ctx, 501, "epoch-boundary", "time-boundary"); !errors.Is(err, ErrUnavailable) || n != nil {
				t.Fatalf("invalid timestamp escaped replay: %v %v", n, err)
			}
		}
	})
	if err := Check(ctx, runtime); err != nil {
		t.Fatal("restored timestamp contract rejected", err)
	}
	if n, err := s.Latest(ctx, 901); err != nil || n == nil || n.PublishedAt != MaxPublishedAt {
		t.Fatalf("upper boundary unavailable after restore: %v %v", n, err)
	}
}
