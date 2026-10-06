//go:build !windows

package appcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

// Every connection below belongs to a newly initialized disposable cluster.
// No configured application DSN or previously running root/test server is used.
func TestProductionMerchantStoreFenceActualPostgres(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for isolated fence qualification")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb requires an unprivileged test process")
	}
	for _, tool := range []string{"initdb", "pg_ctl", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("isolated PostgreSQL tools unavailable")
		}
	}
	for _, assignment := range os.Environ() {
		key, _, found := strings.Cut(assignment, "=")
		if found && strings.HasPrefix(key, "PG") {
			t.Setenv(key, "")
		}
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	socket, err := os.MkdirTemp("/tmp", "lmm-merchant-fence-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	run := func(binary string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		body, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("isolated %s failed: %v\n%s", binary, err, body)
		}
		return body
	}
	run("initdb", "-D", data, "--auth=trust", "--no-locale", "--encoding=UTF8", "--username=lmm_fence_test")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", fmt.Sprintf("-h 127.0.0.1 -k %s -p %d", socket, port), "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	adminDSN := fmt.Sprintf("postgres://lmm_fence_test@127.0.0.1:%d/postgres?sslmode=disable", port)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	_, err = admin.Exec(ctx, `CREATE SCHEMA merchant_fence;
CREATE TABLE merchant_fence.options(key text PRIMARY KEY,value text NOT NULL);
INSERT INTO merchant_fence.options VALUES('MerchantStoreMinimumWriterCapability','4');
CREATE ROLE lmm_fence_business LOGIN;
GRANT USAGE ON SCHEMA merchant_fence TO lmm_fence_business;
GRANT SELECT,INSERT,DELETE ON merchant_fence.options TO lmm_fence_business;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO lmm_fence_business;`)
	if err != nil {
		t.Fatal(err)
	}
	contract := testMerchantWriterContract()
	contract.Database = "postgres"
	contract.Schema = "merchant_fence"
	contract.Role = "lmm_fence_business"
	contract.Candidate.PayloadSHA256, err = sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	err = admin.QueryRow(ctx, `SELECT system_identifier::text,(SELECT oid::bigint FROM pg_database WHERE datname='postgres'),
(SELECT oid::bigint FROM pg_namespace WHERE nspname='merchant_fence') FROM pg_control_system()`).Scan(&contract.SystemIdentifier, &contract.DatabaseOID, &contract.SchemaOID)
	if err != nil {
		t.Fatal(err)
	}
	unixDSN := "postgres://lmm_fence_business@/postgres?host=" + url.QueryEscape(socket) + "&port=" + strconv.Itoa(port) + "&sslmode=disable"
	values := map[string]string{"SQL_DSN": unixDSN}
	lease, err := acquireMerchantStoreDeploymentFence(ctx, values, contract)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := lease.Check(ctx, contract); err != nil {
		t.Fatal(err)
	}
	var exclusive bool
	if err := admin.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", deploymentfence.AdvisoryKey).Scan(&exclusive); err != nil || exclusive {
		t.Fatalf("activation entered a live shared deployment fence: lock=%t error=%v", exclusive, err)
	}
	var migration bool
	const migrationKey int64 = 0x4c4d4d4150490001
	if err := admin.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", migrationKey).Scan(&migration); err != nil || !migration {
		t.Fatalf("separate migration/VERIFY key self-locked: lock=%t error=%v", migration, err)
	}
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationKey); err != nil {
		t.Fatal(err)
	}
	// A TCP session proves the same physical database/schema/business role.
	tcpValues := map[string]string{"SQL_DSN": fmt.Sprintf("postgres://lmm_fence_business@127.0.0.1:%d/postgres?sslmode=disable", port)}
	second, err := acquireMerchantStoreDeploymentFence(ctx, tcpValues, contract)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: "release-fence-test", Host: "arch-dmit", PlanSHA256: strings.Repeat("a", 64), ContractSHA256: merchantStoreFenceContractSHA(contract), ProviderSHA256: contract.Candidate.PayloadSHA256, Nonce: strings.Repeat("d", 32), HolderPID: os.Getpid(), HolderUnit: merchantStoreFenceUnit("release-fence-test"), HolderInvocationID: strings.Repeat("e", 32), BackendPID: lease.backendPID, SystemIdentifier: contract.SystemIdentifier, Database: contract.Database, DatabaseOID: contract.DatabaseOID, Schema: contract.Schema, SchemaOID: contract.SchemaOID, Role: contract.Role}
	for _, change := range []func(*productionMerchantStoreFenceOwner){
		func(o *productionMerchantStoreFenceOwner) { o.ContractSHA256 = strings.Repeat("2", 64) },
		func(o *productionMerchantStoreFenceOwner) { o.ProviderSHA256 = strings.Repeat("3", 64) },
	} {
		invalid := owner
		change(&invalid)
		if err := lease.ClaimOwner(ctx, invalid); err == nil {
			t.Fatal("owner claim accepted a different immutable contract/provider")
		}
	}
	if err := lease.ClaimOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := lease.CheckOwner(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lease.ClaimOwner(ctx, owner); err == nil {
		t.Fatal("owner claim overwrote an existing ACTIVE record")
	}
	t.Run("actual Unix peer and physical PG session RPC", func(t *testing.T) {
		testMerchantStoreFenceLiveRPC(t, ctx, lease, owner, contract)
	})
	if err := lease.ReleaseOwner(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM merchant_fence.options").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("normal owner release changed unrelated options: rows=%d error=%v", rows, err)
	}
	if err := lease.ClaimOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	original := lease.ownerValue
	_, err = admin.Exec(ctx, "UPDATE merchant_fence.options SET value='unknown-owner' WHERE key=$1", lease.ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.CheckOwner(ctx) == nil || lease.ReleaseOwner(ctx) == nil {
		t.Fatal("altered durable owner was cleared or accepted")
	}
	_, err = admin.Exec(ctx, "UPDATE merchant_fence.options SET value=$1 WHERE key=$2", original, lease.ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	} // Deliberate guardian loss, no CAS cleanup.
	if lease.Check(ctx, contract) == nil || lease.ReleaseOwner(ctx) == nil {
		t.Fatal("closed guardian session regained ownership")
	}
	if err := admin.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", deploymentfence.AdvisoryKey).Scan(&exclusive); err != nil || !exclusive {
		t.Fatalf("closed session leaked fence: lock=%t error=%v", exclusive, err)
	}
	var blocker bool
	presence := "SELECT EXISTS(SELECT 1 FROM merchant_fence.options WHERE " + deploymentfence.PostgreSQLPresencePredicate + ")"
	if err := admin.QueryRow(ctx, presence).Scan(&blocker); err != nil || !blocker {
		t.Fatalf("guardian death lost durable activation blocker: blocker=%t error=%v", blocker, err)
	}
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_unlock($1)", deploymentfence.AdvisoryKey); err != nil {
		t.Fatal(err)
	}
	// The production release API never sweeps this orphan. Test cleanup is
	// intentionally limited to the exact record inside this disposable cluster.
	_, err = admin.Exec(ctx, "DELETE FROM merchant_fence.options WHERE key=$1 AND value=$2", merchantStoreFenceOwnerKey(owner), original)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{" MERCHANTSTOREDEPLOYMENTFENCE:orphan ", "\tmerchantstoredeploymentfence:unknown\n", "\u00a0MerchantStoreDeploymentFence:unicode\u3000"} {
		if _, err := admin.Exec(ctx, "INSERT INTO merchant_fence.options VALUES($1,'garbage')", key); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctx, presence).Scan(&blocker); err != nil || !blocker {
			t.Fatalf("noncanonical orphan bypassed activation: key=%q blocker=%t error=%v", key, blocker, err)
		}
		if _, err := admin.Exec(ctx, "DELETE FROM merchant_fence.options WHERE key=$1", key); err != nil {
			t.Fatal(err)
		}
	}
	// A forced disconnect is not silently replaced with a new connection.
	lost, err := acquireMerchantStoreDeploymentFence(ctx, values, contract)
	if err != nil {
		t.Fatal(err)
	}
	defer lost.Close()
	var terminated bool
	if err := admin.QueryRow(ctx, "SELECT pg_terminate_backend($1)", lost.backendPID).Scan(&terminated); err != nil || !terminated {
		t.Fatal("failed to terminate disposable own fence session")
	}
	if lost.Check(ctx, contract) == nil {
		t.Fatal("lost dedicated session silently passed ownership")
	}
	// Same credentials with the identity function revoked are BLOCKED. There
	// is no automatic privilege grant in production acquisition.
	if _, err := admin.Exec(ctx, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM lmm_fence_business"); err != nil {
		t.Fatal(err)
	}
	if invalid, err := acquireMerchantStoreDeploymentFence(ctx, values, contract); err == nil {
		_ = invalid.Close()
		t.Fatal("missing business-role identity privilege was ignored")
	}
	// Keep the complete floor option unchanged throughout owner bookkeeping.
	var floor string
	if err := admin.QueryRow(ctx, "SELECT value FROM merchant_fence.options WHERE key='MerchantStoreMinimumWriterCapability'").Scan(&floor); err != nil || floor != "4" {
		t.Fatalf("fence changed capability floor: %q %v", floor, err)
	}
}

func TestProductionMerchantStoreFenceOwnerCanonicalAndAmbientOverrides(t *testing.T) {
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: "release-fence-test", Host: "arch-dmit", PlanSHA256: strings.Repeat("a", 64), ContractSHA256: strings.Repeat("b", 64), ProviderSHA256: strings.Repeat("c", 64), Nonce: strings.Repeat("d", 32), HolderPID: 123, HolderUnit: merchantStoreFenceUnit("release-fence-test"), HolderInvocationID: strings.Repeat("e", 32), BackendPID: 321, SystemIdentifier: "7648633982160478129", Database: "postgres", DatabaseOID: 1, Schema: "merchant_fence", SchemaOID: 2, Role: "lmm_fence_business"}
	body, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseMerchantStoreFenceOwner(body); err != nil {
		t.Fatal(err)
	}
	for _, mutated := range [][]byte{append(append([]byte{}, body...), []byte("{}")...), []byte(strings.Replace(string(body), "}", `,"state":"ACTIVE"}`, 1)), []byte(strings.Replace(string(body), `"state":"ACTIVE"`, `"state":"RELEASED"`, 1)), []byte(strings.Replace(string(body), "}", `,"unknown":true}`, 1))} {
		if _, err := parseMerchantStoreFenceOwner(mutated); err == nil {
			t.Fatal("noncanonical/unknown owner accepted")
		}
	}
	t.Setenv("PGHOST", "unsealed.invalid")
	if rejectMerchantStoreAmbientPGEnvironment(map[string]string{}) == nil {
		t.Fatal("ambient PG host override entered fence connection")
	}
	if rejectMerchantStoreAmbientPGEnvironment(map[string]string{"PGHOST": "unsealed.invalid"}) != nil {
		t.Fatal("exact sealed PostgreSQL setting rejected")
	}
	t.Setenv("PGHOST", "")
	if rejectMerchantStoreAmbientPGEnvironment(map[string]string{"PGPASSFILE": "/tmp/unsealed-passwords"}) == nil {
		t.Fatal("unsealed external credentials file accepted")
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil || value["state"] != "ACTIVE" {
		t.Fatal("owner wire contract changed")
	}
}
