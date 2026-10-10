//go:build !windows

package deploycli

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

// This proves actual role/READ ONLY/missing-floor capture and the reused
// physical shared-session/ACTIVE/CAS primitive on a NEW owned cluster. It does
// not qualify a fixture ELF as official or claim production 15->28 migration.
func TestProductionMerchantStartupBaselineActualPostgres(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for own isolated PostgreSQL")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb requires unprivileged test process")
	}
	for _, tool := range []string{"initdb", "pg_ctl", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("owned PostgreSQL tools unavailable")
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
	socket, err := os.MkdirTemp("/tmp", "lmm-baseline-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	run := func(tool string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned %s: %v %s", tool, err, raw)
		}
	}
	run("initdb", "-D", data, "--auth=trust", "--no-locale", "--encoding=UTF8", "--username=lmm_baseline_admin")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", fmt.Sprintf("-h 127.0.0.1 -k %s -p %d", socket, port), "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adminDSN := fmt.Sprintf("postgres://lmm_baseline_admin@127.0.0.1:%d/postgres?sslmode=disable", port)
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	ddl := `CREATE SCHEMA baseline_store;CREATE TABLE baseline_store.options(key text PRIMARY KEY,value text NOT NULL);
CREATE ROLE lmm_baseline_business LOGIN;ALTER ROLE lmm_baseline_business SET search_path=baseline_store;
GRANT USAGE ON SCHEMA baseline_store TO lmm_baseline_business;GRANT SELECT,INSERT,DELETE ON baseline_store.options TO lmm_baseline_business;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM PUBLIC;GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO lmm_baseline_business;`
	for i := 0; i < 15; i++ {
		ddl += fmt.Sprintf("CREATE TABLE baseline_store.merchant_store_legacy_%02d(id bigint);", i)
	}
	if _, err := admin.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)
	values := map[string]string{"SQL_DSN": fmt.Sprintf("postgres://lmm_baseline_business@127.0.0.1:%d/postgres?sslmode=disable", port)}
	db, err := captureMerchantStartupDatabase(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	if len(db.FloorValues) != 0 || !db.PreVariant || db.Role != "lmm_baseline_business" || db.OtherClients != 0 || db.ReservedOwners {
		t.Fatalf("actual missing capture=%+v", db)
	}
	other, err := pgx.Connect(ctx, values["SQL_DSN"])
	if err != nil {
		t.Fatal(err)
	}
	busy, err := captureMerchantStartupDatabase(ctx, values)
	if err != nil || busy.OtherClients != 1 {
		t.Fatalf("actual remaining writer count=%d err=%v", busy.OtherClients, err)
	}
	other.Close(ctx)
	admin, err = pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO baseline_store.options VALUES('MerchantStoreMinimumWriterCapability','1')`); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)
	db, err = captureMerchantStartupDatabase(ctx, values)
	if err != nil || !stringsEqual(db.FloorValues, []string{"1"}) || db.OtherClients != 0 {
		t.Fatalf("explicit missing->1 capture=%+v err=%v", db, err)
	}
	// A legitimate URI override must select the same schema for pgx capture
	// and libpq, even when the inspected PGOPTIONS is nonempty and different.
	runtime := productionRuntime{runner: osProductionCommandRunner{}}
	for _, test := range []struct{ name, uriOptions, literalValue string }{
		{"percent-options", "-csearch_path%3Dbaseline_store%20-cdefault_transaction_read_only%3Doff", ""},
		{"form-options-literal-plus", url.QueryEscape("-csearch_path=baseline_store -cdefault_transaction_read_only=off -clmm.adapter_literal=uri+override"), "uri+override"},
		{"percent-options-literal-plus", "-csearch_path%3Dbaseline_store%20-cdefault_transaction_read_only%3Doff%20-clmm.adapter_literal%3Duri%2Boverride", "uri+override"},
		{"empty-options", "", ""},
	} {
		override := map[string]string{"SQL_DSN": values["SQL_DSN"] + "&options=" + test.uriOptions, "PGOPTIONS": "-csearch_path=public -cdefault_transaction_read_only=on"}
		originalDSN := override["SQL_DSN"]
		captured, err := captureMerchantStartupDatabase(ctx, override)
		if err != nil || captured.Schema != db.Schema || captured.Role != db.Role || !stringsEqual(captured.FloorValues, db.FloorValues) {
			t.Fatalf("actual %s override capture=%+v err=%v", test.name, captured, err)
		}
		dsn, environment, err := productionSealedDatabaseCommand(override)
		if err != nil {
			t.Fatal(err)
		}
		query := "SELECT current_schema(),current_setting('default_transaction_read_only')"
		want := "baseline_store|off"
		if test.literalValue != "" {
			query += ",current_setting('lmm.adapter_literal')"
			want += "|" + test.literalValue
		}
		output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-qAt", "-v", "ON_ERROR_STOP=1", "--dbname", dsn, "--command", query}, Env: environment, Sensitive: true})
		if err != nil || strings.TrimSpace(string(output)) != want {
			t.Fatalf("actual libpq %s override=%q err=%v", test.name, output, err)
		}
		if override["SQL_DSN"] != originalDSN || !containsString(environment, "SQL_DSN="+originalDSN) || override["PGOPTIONS"] != "-csearch_path=public -cdefault_transaction_read_only=on" {
			t.Fatal("native adapter changed the sealed pgx environment")
		}
	}
	unknownDSN, unknownEnvironment, err := productionSealedDatabaseCommand(map[string]string{"SQL_DSN": values["SQL_DSN"] + "&options=-csearch_path%3Dbaseline_store&future_setting=private+value"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := exec.CommandContext(ctx, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "--dbname", unknownDSN, "--command", "SELECT 1")
	unknown.Env = unknownEnvironment
	if output, err := unknown.CombinedOutput(); err == nil || !strings.Contains(string(output), `invalid URI query parameter: "future_setting"`) {
		t.Fatal("libpq did not reject the preserved unknown URI parameter")
	}
	// Capture the actual catalog independently, then verify it through the
	// production writer->capsule psql path using the very same fenced child.
	runtime.paths.ConfigDir = root
	if err := os.WriteFile(filepath.Join(root, "lmm-api-go.env"), []byte("SQL_DSN="+values["SQL_DSN"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	actualContract, err := runtime.captureExistingSchemaContract(ctx, db.Schema)
	if err != nil {
		t.Fatal(err)
	}
	writer := &productionMerchantStoreWriterContract{RequiredCapability: 1, SystemIdentifier: db.SystemIdentifier, Database: db.Database, DatabaseOID: db.DatabaseOID, Schema: db.Schema, SchemaOID: db.SchemaOID, Role: db.Role}
	fencedChild, err := runtime.merchantStoreWriterEnvironmentFromValues(ctx, writer, values)
	if err != nil {
		t.Fatal(err)
	}
	capsule := productionMerchantStoreCapsule{Writer: writer, ExistingSchemaContract: &actualContract}
	if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, capsule, fencedChild); err != nil {
		t.Fatalf("actual capsule libpq schema verification: %v", err)
	}
	fencedValues := map[string]string{}
	for _, assignment := range fencedChild {
		key, value, _ := strings.Cut(assignment, "=")
		fencedValues[key] = value
	}
	fencedDSN, fencedEnvironment, err := productionSealedDatabaseCommand(fencedValues)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-qAt", "-v", "ON_ERROR_STOP=1", "--dbname", fencedDSN, "--command", "SELECT current_schema(),current_setting('default_transaction_read_only')"}, Env: fencedEnvironment, Sensitive: true})
	if err != nil || strings.TrimSpace(string(output)) != "baseline_store|on" {
		t.Fatalf("actual capsule child schema/read-only=%q err=%v", output, err)
	}
	write := exec.CommandContext(ctx, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "--dbname", fencedDSN, "--command", "INSERT INTO baseline_store.options VALUES('capsule_write_must_fail','1')")
	write.Env = fencedEnvironment
	if output, err := write.CombinedOutput(); err == nil || !strings.Contains(string(output), "read-only transaction") {
		t.Fatalf("actual capsule child write was not denied by read-only fence: %v %s", err, output)
	}
	admin, err = pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "ALTER TABLE baseline_store.merchant_store_legacy_00 ADD COLUMN capsule_drift bigint"); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)
	if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, capsule, fencedChild); err == nil || err.Error() != "portable physical identity/schema metadata drifted" {
		t.Fatalf("actual capsule accepted catalog drift: %v", err)
	}
	t.Log("actual_capsule_psql=true sealed_child_schema=baseline_store sealed_child_read_only=on write_rejected=true catalog_drift_rejected=true uri_options_override_pgoptions=true empty_uri_options_override_pgoptions=true form_uri_options_round_trip=true literal_plus_preserved=true unknown_uri_parameter_rejected=true raw_sealed_environment_preserved=true")
	c := testMerchantStartupBaseline(t, defaultProductionPaths())
	b := c.Baseline
	b.Role = db.Role
	elf, err := sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	b.Provider.PayloadSHA256 = elf
	b.Closure.ProviderSHA256 = elf
	for i := range b.Closure.Hosts {
		h := &b.Closure.Hosts[i]
		h.ProviderSHA256 = elf
		h.Role = db.Role
		h.Schema.SystemIdentifier = db.SystemIdentifier
		h.Schema.Database = db.Database
		h.Schema.DatabaseOID = db.DatabaseOID
		h.Schema.Schema = db.Schema
		h.Schema.SchemaOID = db.SchemaOID
	}
	w := b.writerIdentity("arch-dmit")
	if _, err := acquireMerchantStoreDeploymentFence(ctx, values, w); err == nil {
		t.Fatal("ordinary contract accepted startup-only identity")
	}
	lease, err := acquireMerchantStartupBaselineFence(ctx, values, b, "arch-dmit")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	admin, err = pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	var exclusive bool
	if err := admin.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", deploymentfence.AdvisoryKey).Scan(&exclusive); err != nil || exclusive {
		t.Fatal("activation entered actual baseline shared session")
	}
	id := "baseline-pg-owned"
	inv := strings.Repeat("c", 32)
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: id, Host: "arch-dmit", PlanSHA256: strings.Repeat("a", 64), ContractSHA256: merchantStoreFenceContractSHA(w), ProviderSHA256: elf, Nonce: strings.Repeat("b", 32), HolderPID: os.Getpid(), HolderUnit: merchantStoreStartUnit(id, inv), HolderInvocationID: strings.Repeat("d", 32), BackendPID: lease.backendPID, SystemIdentifier: w.SystemIdentifier, Database: w.Database, DatabaseOID: w.DatabaseOID, Schema: w.Schema, SchemaOID: w.SchemaOID, Role: w.Role, Purpose: "start", Service: "lmm-api.service", StartInvocationID: inv}
	bad := owner
	bad.Purpose = "portable-deploy"
	bad.StartInvocationID = ""
	bad.HolderUnit = merchantStorePortableUnit(id)
	if lease.ClaimOwner(ctx, bad) == nil {
		t.Fatal("baseline lease authorized ordinary owner")
	}
	if err := lease.ClaimOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	second, err := acquireMerchantStartupBaselineFence(ctx, values, b, "arch-dmit")
	if err != nil {
		t.Fatal(err)
	}
	otherOwner := owner
	otherOwner.Nonce = strings.Repeat("e", 32)
	otherOwner.BackendPID = second.backendPID
	if second.ClaimOwner(ctx, otherOwner) == nil {
		t.Fatal("second actual connection overwrote ACTIVE")
	}
	second.Close()
	ownerJSON, _ := canonicalMerchantStoreFenceOwner(owner)
	if _, err := admin.Exec(ctx, "UPDATE baseline_store.options SET value='tampered' WHERE key=$1", merchantStoreFenceOwnerKey(owner)); err != nil {
		t.Fatal(err)
	}
	if lease.ReleaseOwner(ctx) == nil {
		t.Fatal("wrong original-value CAS deleted ACTIVE")
	}
	if _, err := admin.Exec(ctx, "UPDATE baseline_store.options SET value=$2 WHERE key=$1", merchantStoreFenceOwnerKey(owner), string(ownerJSON)); err != nil {
		t.Fatal(err)
	}
	if err := lease.ReleaseOwner(ctx); err != nil {
		t.Fatal(err)
	}
	// The post-operation check uses THIS same physical session. Even a direct
	// test-admin mutation which bypasses activation's fence cannot be mistaken
	// for the originally sealed floor at a later admission reopen boundary.
	if _, err := admin.Exec(ctx, "UPDATE baseline_store.options SET value='2' WHERE key='MerchantStoreMinimumWriterCapability'"); err != nil {
		t.Fatal(err)
	}
	if lease.Check(ctx, w) == nil {
		t.Fatal("same live session accepted floor change between operation checks")
	}
	if _, err := admin.Exec(ctx, "UPDATE baseline_store.options SET value='1' WHERE key='MerchantStoreMinimumWriterCapability'"); err != nil {
		t.Fatal(err)
	}
	if err := lease.Check(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := lease.ClaimOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	backend := lease.backendPID
	lease.Close()
	if err := admin.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", deploymentfence.AdvisoryKey).Scan(&exclusive); err != nil || !exclusive {
		t.Fatal("closed physical connection retained session lock")
	}
	admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", deploymentfence.AdvisoryKey)
	var rows int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM baseline_store.options WHERE key=$1", merchantStoreFenceOwnerKey(owner)).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("crash residue was auto-deleted")
	}
	if _, err := admin.Exec(ctx, "DELETE FROM baseline_store.options WHERE key=$1 AND value=$2", merchantStoreFenceOwnerKey(owner), string(ownerJSON)); err != nil {
		t.Fatal(err)
	} // Explicit test-only reviewed cleanup.
	if _, err := admin.Exec(ctx, "UPDATE baseline_store.options SET value='2' WHERE key='MerchantStoreMinimumWriterCapability'"); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireMerchantStartupBaselineFence(ctx, values, b, "arch-dmit"); err == nil {
		t.Fatal("stale baseline auto-followed a changed floor")
	}
	if _, err := admin.Exec(ctx, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM lmm_baseline_business"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureMerchantStartupDatabase(ctx, values); err == nil {
		t.Fatal("business role missing identity permission was inferred from admin")
	}
	t.Logf("owned_port=%d actual_business_role=%s actual_fence_backend=%d missing_gate_then_explicit_one=true remaining_client_detected=true crash_ACTIVE_preserved=true exact_CAS=true stale_floor_rejected=true", port, db.Role, backend)
}
