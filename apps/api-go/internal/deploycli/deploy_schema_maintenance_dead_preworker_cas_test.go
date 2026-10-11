//go:build !windows

package deploycli

import (
	"context"
	"errors"
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

// Every database used by these tests is newly initialized by this process,
// owns a private Unix socket and has TCP disabled. The opt-in never accepts a
// configured application DSN. Runtime intent/result persistence is exercised
// separately; this file qualifies only the actual transactional CAS primitive.
type schemaMaintenanceDeadPreWorkerPGFixture struct {
	t        *testing.T
	ctx      context.Context
	adminDSN string
	lease    *productionMerchantStoreFence
	contract *productionMerchantStoreWriterContract
}

func newSchemaMaintenanceDeadPreWorkerPGFixture(t *testing.T) *schemaMaintenanceDeadPreWorkerPGFixture {
	t.Helper()
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for an owned no-TCP dead-preworker CAS cluster")
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
	socket, err := os.MkdirTemp("/tmp", "lmm-dead-pre-")
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
	run("initdb", "-D", data, "--auth-local=trust", "--auth-host=reject", "--no-locale", "--encoding=UTF8", "--username=lmm_dead_pre_admin")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", "-c listen_addresses= -c unix_socket_directories="+socket+" -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off", "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "immediate", "-w", "-t", "20", "stop") })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	f := &schemaMaintenanceDeadPreWorkerPGFixture{t: t, ctx: ctx, adminDSN: "postgres://lmm_dead_pre_admin@/postgres?host=" + url.QueryEscape(socket) + "&sslmode=disable"}
	admin := f.admin()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA dead_pre;
CREATE TABLE dead_pre.options(key text PRIMARY KEY,value text);
INSERT INTO dead_pre.options VALUES('MerchantStoreMinimumWriterCapability','4'),('ordinary_option','untouched');
CREATE TABLE dead_pre.credits(id bigint PRIMARY KEY,quota bigint NOT NULL);
INSERT INTO dead_pre.credits VALUES(1,500000),(2,1000000);
CREATE ROLE lmm_dead_pre_business LOGIN;
ALTER ROLE lmm_dead_pre_business SET default_transaction_isolation='repeatable read';
GRANT USAGE ON SCHEMA dead_pre TO lmm_dead_pre_business;
GRANT SELECT,INSERT,DELETE ON dead_pre.options TO lmm_dead_pre_business;
GRANT SELECT ON dead_pre.credits TO lmm_dead_pre_business;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO lmm_dead_pre_business;`); err != nil {
		t.Fatal(err)
	}
	contract := testMerchantWriterContract()
	contract.Database, contract.Schema, contract.Role = "postgres", "dead_pre", "lmm_dead_pre_business"
	if err := admin.QueryRow(ctx, `SELECT system_identifier::text,(SELECT oid::bigint FROM pg_database WHERE datname='postgres'),(SELECT oid::bigint FROM pg_namespace WHERE nspname='dead_pre') FROM pg_control_system()`).Scan(&contract.SystemIdentifier, &contract.DatabaseOID, &contract.SchemaOID); err != nil {
		t.Fatal(err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"SQL_DSN": "postgres://lmm_dead_pre_business@/postgres?host=" + url.QueryEscape(socket) + "&sslmode=disable"}
	f.lease, err = openMerchantStoreDeploymentFence(ctx, values, contract, false)
	if err != nil {
		t.Fatal(err)
	}
	f.contract = contract
	t.Cleanup(func() { _ = f.lease.Close() })
	var listen string
	if err := f.lease.connection.QueryRow(ctx, "SHOW listen_addresses").Scan(&listen); err != nil || listen != "" {
		t.Fatalf("test cluster exposed TCP: %q %v", listen, err)
	}
	return f
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) admin() *pgx.Conn {
	f.t.Helper()
	c, err := pgx.Connect(f.ctx, f.adminDSN)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) mutate(sql string, args ...any) {
	f.t.Helper()
	c := f.admin()
	defer c.Close(context.Background())
	if _, err := c.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) snapshot() productionExistingSchemaContract {
	f.t.Helper()
	query := strings.TrimSuffix(strings.TrimPrefix(existingSchemaMetadataQuery(f.contract.Schema), "BEGIN READ ONLY;\nSET LOCAL search_path TO pg_catalog;\n"), "\nROLLBACK;")
	tx, err := f.lease.connection.BeginTx(f.ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(f.ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
		f.t.Fatal(err)
	}
	var raw []byte
	if err := tx.QueryRow(f.ctx, query).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Rollback(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	s, err := decodeExistingSchemaSnapshot(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) reset() {
	f.t.Helper()
	f.mutate(`DELETE FROM dead_pre.options WHERE ` + deploymentfence.PostgreSQLPresencePredicate)
	f.mutate(`UPDATE dead_pre.options SET value='4' WHERE key='MerchantStoreMinimumWriterCapability'`)
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) owner() (productionMerchantStoreFenceOwner, []byte) {
	f.t.Helper()
	c := f.admin()
	pid := int32(c.PgConn().PID())
	if err := c.Close(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	owner := testSchemaMaintenanceOwner()
	owner.SystemIdentifier, owner.Database, owner.DatabaseOID, owner.Schema, owner.SchemaOID, owner.Role = f.contract.SystemIdentifier, f.contract.Database, f.contract.DatabaseOID, f.contract.Schema, f.contract.SchemaOID, f.contract.Role
	owner.BackendPID = pid
	owner.ContractSHA256 = merchantStoreFenceContractSHA(f.contract)
	owner.ProviderSHA256 = f.contract.Candidate.PayloadSHA256
	raw, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		f.t.Fatal(err)
	}
	return owner, raw
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) seed(owner productionMerchantStoreFenceOwner, raw []byte) {
	f.t.Helper()
	f.mutate(`INSERT INTO dead_pre.options VALUES($1,$2)`, merchantStoreFenceOwnerKey(owner), string(raw))
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) options() string {
	f.t.Helper()
	var v string
	if err := f.lease.connection.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY key),'[]'::jsonb)::text FROM dead_pre.options t`).Scan(&v); err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) normalOptions() string {
	f.t.Helper()
	var v string
	if err := f.lease.connection.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY key),'[]'::jsonb)::text FROM dead_pre.options t WHERE (`+deploymentfence.PostgreSQLPresencePredicate+`) IS NOT TRUE`).Scan(&v); err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) money() string {
	f.t.Helper()
	var v string
	if err := f.lease.connection.QueryRow(f.ctx, `SELECT jsonb_agg(to_jsonb(t) ORDER BY id)::text FROM dead_pre.credits t`).Scan(&v); err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *schemaMaintenanceDeadPreWorkerPGFixture) unchanged(options, money string) {
	f.t.Helper()
	if f.options() != options || f.money() != money {
		f.t.Fatal("failed CAS changed exact options or historical fixture money")
	}
}

func TestSchemaMaintenanceDeadPreWorkerCASActualPostgres(t *testing.T) {
	f := newSchemaMaintenanceDeadPreWorkerPGFixture(t)
	t.Run("exact sole dead owner removed", func(t *testing.T) {
		f.t = t
		f.reset()
		expected := f.snapshot()
		owner, raw := f.owner()
		f.seed(owner, raw)
		money := f.money()
		normal := f.normalOptions()
		calls := 0
		if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, "EXACT_ORIGINAL_OWNER", func(context.Context) error { calls++; return nil }); err != nil {
			t.Fatal(err)
		}
		var exists bool
		if err := f.lease.connection.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM dead_pre.options WHERE `+deploymentfence.PostgreSQLPresencePredicate+`)`).Scan(&exists); err != nil || exists {
			t.Fatalf("exact original owner still present: %v", err)
		}
		if calls == 0 || f.money() != money || f.normalOptions() != normal || f.snapshot() != expected {
			t.Fatal("successful exact CAS changed old catalog/money or skipped local guard")
		}
	})
	t.Run("explicit zero owner preserves every row", func(t *testing.T) {
		f.t = t
		f.reset()
		f.mutate(`CREATE FUNCTION public.dead_pre_forbid_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'ZERO_OWNER issued DELETE'; END$$;
CREATE TRIGGER no_zero_delete BEFORE DELETE ON dead_pre.options FOR EACH STATEMENT EXECUTE FUNCTION public.dead_pre_forbid_delete()`)
		t.Cleanup(func() {
			f.mutate(`DROP TRIGGER no_zero_delete ON dead_pre.options; DROP FUNCTION public.dead_pre_forbid_delete()`)
		})
		expected := f.snapshot()
		options, money := f.options(), f.money()
		if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, nil, "ZERO_OWNER", func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		f.unchanged(options, money)
	})
	for _, scenario := range []string{"wrong exact value", "extra unknown owner", "NULL exact owner", "NULL foreign reserved owner", "abnormal reserved key", "zero with owner", "zero with raw", "unknown mode", "missing exact raw", "noncanonical raw", "live original backend", "self PID reused", "wrong physical schema", "changed floor", "changed catalog", "hidden admin client"} {
		t.Run(scenario, func(t *testing.T) {
			f.t = t
			f.reset()
			expected := f.snapshot()
			owner, raw := f.owner()
			mode := "EXACT_ORIGINAL_OWNER"
			provided := append([]byte{}, raw...)
			var live *pgx.Conn
			switch scenario {
			case "wrong exact value":
				f.seed(owner, []byte("unreviewed-owner"))
			case "extra unknown owner":
				f.seed(owner, raw)
				f.mutate(`INSERT INTO dead_pre.options VALUES('MerchantStoreDeploymentFence:unknown','unknown')`)
			case "NULL exact owner":
				f.mutate(`INSERT INTO dead_pre.options VALUES($1,NULL)`, merchantStoreFenceOwnerKey(owner))
			case "NULL foreign reserved owner":
				f.seed(owner, raw)
				f.mutate(`INSERT INTO dead_pre.options VALUES('MerchantStoreDeploymentFence:unknown',NULL)`)
			case "abnormal reserved key":
				f.seed(owner, raw)
				f.mutate(`INSERT INTO dead_pre.options VALUES(E'\u2003mERCHANTsTOREdEPLOYMENTfENCE:unknown\u00a0',NULL)`)
			case "zero with owner":
				f.seed(owner, raw)
				mode = "ZERO_OWNER"
				provided = nil
			case "zero with raw":
				mode = "ZERO_OWNER"
			case "unknown mode":
				f.seed(owner, raw)
				mode = "CLEAR_ALL"
			case "missing exact raw":
				f.seed(owner, raw)
				provided = nil
			case "noncanonical raw":
				f.seed(owner, raw)
				provided = append([]byte(" "), provided...)
			case "live original backend":
				live = f.admin()
				owner.BackendPID = int32(live.PgConn().PID())
				raw, _ = canonicalMerchantStoreFenceOwner(owner)
				provided = raw
				f.seed(owner, raw)
			case "self PID reused":
				owner.BackendPID = f.lease.backendPID
				raw, _ = canonicalMerchantStoreFenceOwner(owner)
				provided = raw
				f.seed(owner, raw)
			case "wrong physical schema":
				f.seed(owner, raw)
				expected.SchemaOID++
			case "changed floor":
				f.seed(owner, raw)
				f.mutate(`UPDATE dead_pre.options SET value='5' WHERE key='MerchantStoreMinimumWriterCapability'`)
			case "changed catalog":
				f.seed(owner, raw)
				f.mutate(`ALTER TABLE dead_pre.credits ADD COLUMN drift_early text`)
			case "hidden admin client":
				f.seed(owner, raw)
				live = f.admin()
				var kind *string
				if err := f.lease.connection.QueryRow(f.ctx, `SELECT backend_type FROM pg_stat_activity WHERE pid=$1`, live.PgConn().PID()).Scan(&kind); err != nil || kind != nil {
					t.Fatalf("fixture client not privilege redacted: %v", err)
				}
			}
			if live != nil {
				defer live.Close(context.Background())
			}
			before, money := f.options(), f.money()
			calls := 0
			if err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, provided, mode, func(context.Context) error { calls++; return nil }); err == nil {
				t.Fatal("unsafe or stale dead owner CAS accepted")
			}
			f.unchanged(before, money)
		})
	}
	t.Run("guard failure after delete rolls exact original owner back", func(t *testing.T) {
		f.t = t
		f.reset()
		// PostgreSQL sequences intentionally do not roll back. This isolated
		// audit proves that the DELETE reached its row trigger before the
		// local-only guard failed; the business owner must still roll back.
		f.mutate(`CREATE SEQUENCE public.dead_pre_delete_audit;
GRANT USAGE ON SEQUENCE public.dead_pre_delete_audit TO lmm_dead_pre_business;
CREATE FUNCTION public.dead_pre_note_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM nextval('public.dead_pre_delete_audit'); RETURN OLD; END$$;
CREATE TRIGGER note_dead_pre_delete BEFORE DELETE ON dead_pre.options FOR EACH ROW EXECUTE FUNCTION public.dead_pre_note_delete()`)
		t.Cleanup(func() {
			f.mutate(`DROP TRIGGER note_dead_pre_delete ON dead_pre.options; DROP FUNCTION public.dead_pre_note_delete(); DROP SEQUENCE public.dead_pre_delete_audit`)
		})
		expected := f.snapshot()
		owner, raw := f.owner()
		f.seed(owner, raw)
		before, money := f.options(), f.money()
		guardErr := errors.New("local stopped authority was lost after DELETE")
		calls := 0
		guard := func(context.Context) error {
			calls++
			if calls >= 3 {
				return guardErr
			}
			return nil
		}
		err := f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(f.ctx, expected, raw, "EXACT_ORIGINAL_OWNER", guard)
		var audited bool
		admin := f.admin()
		auditErr := admin.QueryRow(f.ctx, `SELECT is_called FROM public.dead_pre_delete_audit`).Scan(&audited)
		if err := admin.Close(f.ctx); err != nil {
			t.Fatal(err)
		}
		if err == nil || auditErr != nil || !audited {
			t.Fatalf("fixture did not fail after actual DELETE: audited=%t err=%v audit=%v", audited, err, auditErr)
		}
		f.unchanged(before, money)
	})
	t.Run("late privilege-redacted client after DELETE forces rollback", func(t *testing.T) {
		f.t = t
		f.reset()
		expected := f.snapshot()
		owner, raw := f.owner()
		f.seed(owner, raw)
		before, money := f.options(), f.money()
		atDelete := make(chan struct{})
		resume := make(chan struct{})
		done := make(chan error, 1)
		queryCtx, cancel := context.WithCancel(f.ctx)
		defer cancel()
		calls := 0
		go func() {
			done <- f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(queryCtx, expected, raw, "EXACT_ORIGINAL_OWNER", func(ctx context.Context) error {
				calls++
				if calls == 3 {
					close(atDelete)
					select {
					case <-resume:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			})
		}()
		select {
		case <-atDelete:
		case err := <-done:
			t.Fatalf("CAS never reached its post-DELETE guard: %v", err)
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatal("CAS did not reach post-DELETE guard")
		}
		late := f.admin()
		defer late.Close(context.Background())
		close(resume)
		err := <-done
		if err == nil || !strings.Contains(err.Error(), "another client") {
			t.Fatalf("late client escaped the fresh post-DELETE check: %v", err)
		}
		var kind *string
		if err := f.lease.connection.QueryRow(f.ctx, `SELECT backend_type FROM pg_stat_activity WHERE pid=$1`, late.PgConn().PID()).Scan(&kind); err != nil || kind != nil {
			t.Fatalf("late client was not privilege redacted: %v", err)
		}
		f.unchanged(before, money)
	})

}

func TestSchemaMaintenanceDeadPreWorkerCASWaitsForFreshCatalog(t *testing.T) {
	for _, blocked := range []struct {
		name string
		key  int64
	}{{"claim3", merchantStoreDeploymentOwnerClaimKey}, {"DDL1", int64(0x4c4d4d4150490001)}} {
		for _, mode := range []string{"EXACT_ORIGINAL_OWNER", "ZERO_OWNER"} {
			t.Run(blocked.name+"/"+mode, func(t *testing.T) {
				f := newSchemaMaintenanceDeadPreWorkerPGFixture(t)
				expected := f.snapshot()
				var raw []byte
				if mode == "EXACT_ORIGINAL_OWNER" {
					owner, original := f.owner()
					raw = original
					f.seed(owner, raw)
				}
				blocker := f.admin()
				schemaMaintenanceDeadPreWorkerWaitCatalogChange(t, f.ctx, f.lease, expected, blocker, f.admin, blocked.key, func(ctx context.Context, guard func(context.Context) error) error {
					return f.lease.recoverSchemaMaintenanceDeadPreWorkerOwner(ctx, expected, raw, mode, guard)
				})
			})
		}
	}
}
func schemaMaintenanceDeadPreWorkerWaitCatalogChange(
	t *testing.T,
	ctx context.Context,
	lease *productionMerchantStoreFence,
	expected productionExistingSchemaContract,
	blocker *pgx.Conn,
	connectAdmin func() *pgx.Conn,
	blockingKey int64,
	recover func(context.Context, func(context.Context) error) error,
) {
	t.Helper()
	if recover == nil || connectAdmin == nil || blocker == nil {
		t.Fatal("wait-catalog test lacks its private fixture")
	}
	defer blocker.Close(context.Background())
	options := pgx.Identifier{expected.Schema, "options"}.Sanitize()
	credits := pgx.Identifier{expected.Schema, "credits"}.Sanitize()
	optionsQuery := `SELECT COALESCE(jsonb_agg(jsonb_build_array(key,value) ORDER BY key),'[]'::jsonb)::text FROM ` + options
	creditsQuery := `SELECT COALESCE(jsonb_agg(jsonb_build_array(id,quota) ORDER BY id),'[]'::jsonb)::text FROM ` + credits
	var beforeOptions, beforeCredits string
	if err := blocker.QueryRow(ctx, optionsQuery).Scan(&beforeOptions); err != nil {
		t.Fatal(err)
	}
	if err := blocker.QueryRow(ctx, creditsQuery).Scan(&beforeCredits); err != nil {
		t.Fatal(err)
	}
	// A session lock survives the ALTER TABLE statement's own COMMIT. Keeping
	// this same session until Close avoids the helper racing ahead while the
	// blocker is still an ordinary/privilege-redacted client in the database.
	if _, err := blocker.Exec(ctx, `SELECT pg_catalog.pg_advisory_lock($1)`, blockingKey); err != nil {
		t.Fatal(err)
	}
	queryCtx, queryCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- recover(queryCtx, func(context.Context) error { return nil }) }()
	collected := false
	defer func() {
		queryCancel()
		if !collected {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("dead-owner CAS did not stop after cancellation")
			}
		}
	}()
	const waitBudget = 5 * time.Second
	deadline := time.Now().Add(waitBudget)
	for {
		var blocked bool
		if err := blocker.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM pg_catalog.pg_locks
 WHERE pid=$1 AND locktype='advisory' AND NOT granted
 AND database=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database())
 AND classid::bigint=$2 AND objid::bigint=$3 AND objsubid=1)`,
			lease.backendPID, int64(uint64(blockingKey)>>32), int64(uint64(blockingKey)&0xffffffff)).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			collected = true
			t.Fatalf("CAS returned before actually waiting for %#x: %v", blockingKey, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("CAS never waited for exact advisory key %#x", blockingKey)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Change the complete catalog only AFTER the CAS is proven to be waiting.
	// Exec has no wrapping explicit transaction: this DDL has committed before
	// Close releases the session advisory lock and allows CAS to continue.
	if _, err := blocker.Exec(ctx, `ALTER TABLE `+credits+` ADD COLUMN dead_preworker_wait_drift text`); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var result error
	select {
	case result = <-done:
		collected = true
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A generic error or another-client error would not prove post-lock schema
	// freshness. Require the helper's catalog-drift rejection specifically.
	if result == nil || !strings.Contains(strings.ToLower(result.Error()), "catalog") {
		t.Fatalf("CAS did not reject the catalog committed during its lock wait: %v", result)
	}
	if lease.connection.PgConn().TxStatus() != 'I' {
		t.Fatal("failed CAS did not roll back its actual PostgreSQL transaction")
	}
	// No observer connection existed while CAS advanced. Reconnect only after
	// the helper returned, then prove the exact reserved value and all credits
	// survived the rejected operation, even in EXACT_ORIGINAL_OWNER mode.
	after := connectAdmin()
	defer after.Close(context.Background())
	var afterOptions, afterCredits string
	if err := after.QueryRow(ctx, optionsQuery).Scan(&afterOptions); err != nil {
		t.Fatal(err)
	}
	if err := after.QueryRow(ctx, creditsQuery).Scan(&afterCredits); err != nil {
		t.Fatal(err)
	}
	if beforeOptions != afterOptions || beforeCredits != afterCredits {
		t.Fatal("rejected dead-owner CAS changed a reserved owner, option or credit row")
	}
	var driftColumnPresent bool
	if err := after.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='credits' AND a.attname='dead_preworker_wait_drift' AND NOT a.attisdropped)`, expected.Schema).Scan(&driftColumnPresent); err != nil || !driftColumnPresent {
		t.Fatalf("late DDL was not actually committed: present=%t err=%v", driftColumnPresent, err)
	}
	t.Logf("private_no_tcp=true waited_advisory=%#x ddl_committed_during_wait=true post_wait_catalog_rejected=true exact_reserved_options_preserved=true credits_preserved=true", blockingKey)
}
