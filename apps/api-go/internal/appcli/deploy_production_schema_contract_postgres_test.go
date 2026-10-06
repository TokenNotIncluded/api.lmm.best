//go:build !windows

package appcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Opt-in integration coverage never connects to a configured application
// database: it initializes a new user-owned cluster without a TCP listener.
func TestExistingSchemaPostgresCatalogAndReadOnlyFence(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for isolated PostgreSQL catalog validation")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb requires a non-root test process")
	}
	for _, binary := range []string{"initdb", "pg_ctl", "psql"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip("PostgreSQL test tools unavailable")
		}
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	// Unix socket paths have a small fixed limit; test names and TMPDIR may be
	// long. This private directory contains only this cluster's socket files.
	socket, err := os.MkdirTemp("/tmp", "lmm-schema-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	command := func(binary string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("isolated PostgreSQL %s: %v\n%s", binary, err, out)
		}
		return out
	}
	command("initdb", "-D", data, "-A", "trust", "--no-locale", "-E", "UTF8")
	command("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", "-h '' -k "+socket+" -p 5432", "start")
	t.Cleanup(func() { command("pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	psql := func(sql string) []byte {
		return command("psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-h", socket, "-p", "5432", "-d", "postgres", "-At", "-c", sql)
	}
	psql(`CREATE SCHEMA test_schema;
CREATE TYPE test_schema.edition AS ENUM ('standard','premium');
CREATE TABLE test_schema.stock (id bigserial PRIMARY KEY, quota bigint NOT NULL, edition test_schema.edition DEFAULT 'standard');
CREATE INDEX stock_quota ON test_schema.stock(quota);
ALTER TABLE test_schema.stock ENABLE ROW LEVEL SECURITY;
CREATE POLICY stock_read ON test_schema.stock FOR SELECT USING (quota > 0);
CREATE FUNCTION test_schema.stock_count() RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM test_schema.stock';
CREATE VIEW test_schema.stock_view AS SELECT id,quota FROM test_schema.stock;`)
	capture := func() productionExistingSchemaContract {
		t.Helper()
		contract, err := decodeExistingSchemaSnapshot(psql(existingSchemaMetadataQuery("test_schema")))
		if err != nil {
			t.Fatal(err)
		}
		return contract
	}
	baseline := capture()
	// Loaded or actual service environments must resolve the contracted
	// schema, even when the native operator has a different ambient PGOPTIONS.
	t.Setenv("PGOPTIONS", "-c search_path=public")
	schemaRuntime := productionRuntime{runner: osProductionCommandRunner{}}
	schemaValues := map[string]string{"SQL_DSN": "postgres:///postgres?host=" + socket, "PGOPTIONS": "-c search_path=test_schema"}
	if err := schemaRuntime.verifyExistingSchemaEffectiveSearchPath(context.Background(), schemaValues, &baseline); err != nil {
		t.Fatal(err)
	}
	schemaValues["PGOPTIONS"] = "-c search_path=public"
	if err := schemaRuntime.verifyExistingSchemaEffectiveSearchPath(context.Background(), schemaValues, &baseline); err == nil {
		t.Fatal("actual effective schema mismatch accepted")
	}
	psql("INSERT INTO test_schema.stock(quota) VALUES (100),(200); SELECT nextval('test_schema.stock_id_seq');")
	if actual := capture(); actual != baseline {
		t.Fatal("business writes or sequence position changed immutable schema contract")
	}
	psql("ALTER FUNCTION test_schema.stock_count() COST 200;")
	if actual := capture(); actual.MetadataSHA256 == baseline.MetadataSHA256 {
		t.Fatal("actual routine definition attributes escaped schema drift detection")
	}
	psql("ALTER FUNCTION test_schema.stock_count() COST 100;")
	if actual := capture(); actual != baseline {
		t.Fatal("routine restoration did not restore its logical catalog contract")
	}
	psql("ALTER TABLE test_schema.stock ADD COLUMN description text;")
	drift := capture()
	if drift.MetadataSHA256 == baseline.MetadataSHA256 {
		t.Fatal("actual column DDL escaped schema drift detection")
	}
	psql("GRANT SELECT ON test_schema.stock TO PUBLIC;")
	acl := capture()
	if acl.MetadataSHA256 == drift.MetadataSHA256 {
		t.Fatal("actual ACL change escaped schema drift detection")
	}
	psql("ALTER SEQUENCE test_schema.stock_id_seq OWNED BY NONE;")
	if actual := capture(); actual.MetadataSHA256 == acl.MetadataSHA256 {
		t.Fatal("actual sequence ownership change escaped schema drift detection")
	}
	// A hostile original DSN cannot undo the child's read-only options.
	runtime := productionRuntime{}
	url := "postgres:///postgres?host=" + socket + "&options=-c%20default_transaction_read_only%3Doff"
	environment, err := runtime.existingSchemaMigrationEnvironment([]byte("SQL_DSN="+url+"\n"), "test_schema")
	if err != nil {
		t.Fatal(err)
	}
	var fencedDSN string
	for _, assignment := range environment {
		if strings.HasPrefix(assignment, "SQL_DSN=") {
			fencedDSN = strings.TrimPrefix(assignment, "SQL_DSN=")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, fencedDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	_, err = connection.Exec(ctx, "INSERT INTO test_schema.stock(quota) VALUES (999);")
	if err == nil || !strings.Contains(err.Error(), "read-only transaction") {
		t.Fatalf("actual PostgreSQL accepted a write through fenced verification connection: %v", err)
	}
}
