//go:build !windows

package deploycli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This is an owned no-TCP cluster. In particular, the waiting-claim case uses
// the role's repeatable-read default and a real uncommitted reserved row.
func TestSchemaMaintenanceAbortPreStateActualPostgres(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for an owned abort pre-state qualification")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb requires an ordinary UID")
	}
	for _, name := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("owned PostgreSQL fixture requires %s", name)
		}
	}
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "PG") {
			t.Setenv(key, "")
		}
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	socket, err := os.MkdirTemp("/tmp", "lmm-abort-pre-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	run := func(name string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
			t.Fatalf("owned %s fixture failed: %v %s", name, err, out)
		}
	}
	run("initdb", "-D", data, "--auth-local=trust", "--auth-host=reject", "--no-locale", "--encoding=UTF8", "--username=lmm_abort_admin")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", "-c listen_addresses= -c unix_socket_directories="+socket+" -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off", "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "immediate", "-w", "-t", "20", "stop") })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dsn := "postgres://lmm_abort_admin@/postgres?host=" + url.QueryEscape(socket) + "&sslmode=disable"
	connectAdmin := func() *pgx.Conn {
		t.Helper()
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	admin := connectAdmin()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA abort_app;
CREATE TABLE abort_app.options(key text PRIMARY KEY,value text);
INSERT INTO abort_app.options VALUES('MerchantStoreMinimumWriterCapability','4');
CREATE TABLE abort_app.credits(id bigint PRIMARY KEY,quota bigint NOT NULL);
INSERT INTO abort_app.credits VALUES(1,500000),(2,1000000);
CREATE ROLE lmm_abort_business LOGIN;
ALTER ROLE lmm_abort_business SET default_transaction_isolation='repeatable read';
GRANT USAGE ON SCHEMA abort_app TO lmm_abort_business;
GRANT SELECT ON ALL TABLES IN SCHEMA abort_app TO lmm_abort_business;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO lmm_abort_business;`); err != nil {
		t.Fatal(err)
	}
	contract := testMerchantWriterContract()
	contract.Database, contract.Schema, contract.Role = "postgres", "abort_app", "lmm_abort_business"
	if err := admin.QueryRow(ctx, `SELECT system_identifier::text,(SELECT oid::bigint FROM pg_database WHERE datname='postgres'),(SELECT oid::bigint FROM pg_namespace WHERE nspname='abort_app') FROM pg_control_system()`).Scan(&contract.SystemIdentifier, &contract.DatabaseOID, &contract.SchemaOID); err != nil {
		t.Fatal(err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"SQL_DSN": "postgres://lmm_abort_business@/postgres?host=" + url.QueryEscape(socket) + "&sslmode=disable"}
	lease, err := openMerchantStoreDeploymentFence(ctx, values, contract, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	query := strings.TrimSuffix(strings.TrimPrefix(existingSchemaMetadataQuery(contract.Schema), "BEGIN READ ONLY;\nSET LOCAL search_path TO pg_catalog;\n"), "\nROLLBACK;")
	var raw []byte
	tx, err := lease.connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, query).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	expected, err := decodeExistingSchemaSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.checkSchemaMaintenanceAbortQuiescedPreState(ctx, expected); err != nil {
		t.Fatalf("actual quiesced unchanged old catalog: %v", err)
	}
	// A known old runtime pool is permitted after the initial frozen abort
	// authority, while the initial quiesced preparation still rejects it.
	admin = connectAdmin()
	var hiddenBackendType *string
	if err := lease.connection.QueryRow(ctx, `SELECT backend_type FROM pg_catalog.pg_stat_activity WHERE pid=$1`, admin.PgConn().PID()).Scan(&hiddenBackendType); err != nil || hiddenBackendType != nil {
		t.Fatalf("fixture did not expose a privilege-redacted unknown client: %v", err)
	}
	if err := lease.checkSchemaMaintenanceAbortQuiescedPreState(ctx, expected); err == nil {
		t.Fatal("initial quiesced gate accepted another client")
	}
	guarded, err := schemaMaintenanceOutcomeGuardQuery("SELECT 1;", int(lease.backendPID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.connection.Exec(ctx, guarded); err == nil || !strings.Contains(err.Error(), "schema maintenance outcome has another client") {
		t.Fatalf("outcome guard accepted a privilege-redacted unknown client: %v", err)
	}
	if _, err := lease.connection.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := lease.checkSchemaMaintenanceAbortPreState(ctx, expected); err != nil {
		t.Fatalf("frozen old-runtime gate rejected an existing client: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	mutate := func(sql string) {
		t.Helper()
		conn := connectAdmin()
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	mutate(`INSERT INTO abort_app.options VALUES('MerchantStoreDeploymentFence:unknown',NULL)`)
	if err := lease.checkSchemaMaintenanceAbortPreState(ctx, expected); err == nil || !strings.Contains(err.Error(), "reserved owner") {
		t.Fatalf("NULL unknown reserved owner accepted: %v", err)
	}
	mutate(`DELETE FROM abort_app.options WHERE key='MerchantStoreDeploymentFence:unknown'`)
	// A real claim holds ...0003 while its row is invisible. The gate must
	// wait, then see that row in a fresh READ COMMITTED snapshot after COMMIT.
	admin = connectAdmin()
	claim, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Rollback(context.Background())
	if _, err := claim.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", merchantStoreDeploymentOwnerClaimKey); err != nil {
		t.Fatal(err)
	}
	if _, err := claim.Exec(ctx, `INSERT INTO abort_app.options VALUES('MerchantStoreDeploymentFence:committing','unknown')`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- lease.checkSchemaMaintenanceAbortPreState(ctx, expected) }()
	deadline := time.Now().Add(5 * time.Second)
	waited := false
	for time.Now().Before(deadline) {
		if err := claim.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, lease.backendPID).Scan(&waited); err != nil {
			t.Fatal(err)
		}
		if waited {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waited {
		t.Fatal("abort gate did not wait for the real serialized claim")
	}
	if err := claim.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "reserved owner") {
		t.Fatalf("abort missed the owner committed during its wait: %v", err)
	}
	admin.Close(ctx)
	mutate(`DELETE FROM abort_app.options WHERE key='MerchantStoreDeploymentFence:committing'`)
	mutate(`UPDATE abort_app.options SET value='5' WHERE key='MerchantStoreMinimumWriterCapability'`)
	if err := lease.checkSchemaMaintenanceAbortPreState(ctx, expected); err == nil {
		t.Fatal("abort accepted a changed writer floor")
	}
	mutate(`UPDATE abort_app.options SET value='4' WHERE key='MerchantStoreMinimumWriterCapability'`)
	if err := lease.checkSchemaMaintenanceAbortPreState(ctx, expected); err != nil {
		t.Fatal(err)
	}
	mutate(`ALTER TABLE abort_app.credits ADD COLUMN unexpected text`)
	if err := lease.checkSchemaMaintenanceAbortPreState(ctx, expected); err == nil {
		t.Fatal("abort accepted a changed old catalog")
	}
	admin = connectAdmin()
	defer admin.Close(ctx)
	var quota int64
	if err := admin.QueryRow(ctx, `SELECT sum(quota) FROM abort_app.credits`).Scan(&quota); err != nil || quota != 1500000 {
		t.Fatalf("abort read-only gates changed fixture money: %s", fmt.Sprint(err))
	}
}
