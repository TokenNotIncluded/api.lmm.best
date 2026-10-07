//go:build !windows

package appcli

import (
	"context"
	"fmt"
	"net"
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
	for _, tool := range []string{"initdb", "pg_ctl"} {
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
