package deploycli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

func schemaBackupTestStops(plan productionSchemaMaintenancePlan, sha string, now time.Time) []productionSchemaMaintenanceHostCapture {
	var captures []productionSchemaMaintenanceHostCapture
	for _, origin := range plan.Origins {
		captures = append(captures, productionSchemaMaintenanceHostCapture{Format: 1, TransitionID: plan.TransitionID, PlanSHA256: sha, Host: origin.Host, Phase: "WRITER_STOPPED", CapsuleSHA256: origin.OldCapsule.SHA256, ProviderSHA256: origin.Recovery.Candidate.PayloadSHA256, PID: 101, InvocationID: strings.Repeat("1", 32), OriginalAdmissionSHA256: strings.Repeat("2", 64), ClosedAdmissionSHA256: strings.Repeat("3", 64), StopStartedUTC: now.Add(-time.Minute), ShutdownJournalSHA256: strings.Repeat("4", 64)})
	}
	return captures
}
func TestSchemaMaintenanceBackupProofRejectsWrongGenerationAndRecipeOnly(t *testing.T) {
	runtime, plan, _ := newSchemaMaintenancePlanFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	sha := strings.Repeat("a", 64)
	captures := schemaBackupTestStops(plan, sha, now)
	stops, err := schemaMaintenanceBackupStopBindings(plan, sha, captures)
	if err != nil {
		t.Fatal(err)
	}
	proof := productionSchemaMaintenanceBackupProof{Format: schemaMaintenanceBackupProofFormat, TransitionID: plan.TransitionID, PrePlanSHA256: sha, PreSchema: plan.PreSchema, CapturedUTC: now, VerifiedUTC: now, RecipientSHA256: plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, BackupSetSHA256: sha, Archive: controllerBackupArchive{CiphertextSHA256: sha, PlaintextSHA256: sha, CiphertextBytes: 100, PlaintextBytes: 50}, SourceSHA256: sha, SourceLogicalSHA256: sha, RestoredLogicalSHA256: sha, RestoredSystemIdentifier: "123", RestoredDatabase: "schema_restore", RestoreKind: schemaMaintenanceBackupRestoreKind, ActualRestore: true, NoTCP: true, TableCount: 1, TotalRows: 10}
	if err = validateSchemaMaintenanceBackupProof(proof, plan, sha, stops, now); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*productionSchemaMaintenanceBackupProof){func(p *productionSchemaMaintenanceBackupProof) { p.ActualRestore = false }, func(p *productionSchemaMaintenanceBackupProof) { p.RestoreKind = "pg_restore-list" }, func(p *productionSchemaMaintenanceBackupProof) { p.NoTCP = false }, func(p *productionSchemaMaintenanceBackupProof) { p.PrePlanSHA256 = strings.Repeat("b", 64) }, func(p *productionSchemaMaintenanceBackupProof) { p.RestoredLogicalSHA256 = strings.Repeat("b", 64) }, func(p *productionSchemaMaintenanceBackupProof) {
		p.RestoredSystemIdentifier = plan.PreSchema.SystemIdentifier
	}} {
		changed := proof
		change(&changed)
		if validateSchemaMaintenanceBackupProof(changed, plan, sha, stops, now) == nil {
			t.Fatal("incomplete restore admitted")
		}
	}
	for _, field := range []string{"provider", "original_admission"} {
		changed := append([]productionSchemaMaintenanceHostCapture(nil), captures...)
		if field == "provider" {
			changed[0].ProviderSHA256 = ""
		} else {
			changed[0].OriginalAdmissionSHA256 = ""
		}
		if _, e := schemaMaintenanceBackupStopBindings(plan, sha, changed); e == nil {
			t.Fatal("incomplete stopped provider/admission accepted")
		}
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	payload, _ := json.Marshal(proof)
	raw, _ := json.Marshal(controllerBackupEnvelope{Format: 1, Payload: payload, Signature: hex.EncodeToString(ed25519.Sign(key, payload))})
	if _, err = schemaMaintenanceBackupDecodeProof(raw, plan.BackupPolicy.ControllerPublicKey); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(plan.Origins[0].Workspace, "state", schemaMaintenanceBackupProofName)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	runtime.now = func() time.Time { return now }
	if err = runtime.verifySchemaMaintenanceBackup(context.Background(), plan, sha, captures); err != nil {
		t.Fatal(err)
	}
	captures[1].InvocationID = strings.Repeat("9", 32)
	if runtime.verifySchemaMaintenanceBackup(context.Background(), plan, sha, captures) == nil {
		t.Fatal("other stopped generation admitted")
	}
	raw[len(raw)-3] ^= 1
	if _, err = schemaMaintenanceBackupDecodeProof(raw, plan.BackupPolicy.ControllerPublicKey); err == nil {
		t.Fatal("damaged signature admitted")
	}
}

// Explicit opt-in: source and restore are newly owned no-TCP clusters, never a
// workstation/production DSN. Actual restore also has no host filesystem/network.
func TestSchemaMaintenanceBackupActualRestoreWithRealTools(t *testing.T) {
	if os.Getenv("LMM_SCHEMA_BACKUP_REAL_TOOLS_TEST") != "1" {
		t.Skip("requires explicit owned local PostgreSQL/age/bwrap test opt-in")
	}
	if os.Geteuid() == 0 {
		t.Fatal("real backup fixture must run as ordinary controller UID")
	}
	for _, tool := range []string{"initdb", "pg_ctl", "psql", "pg_dump", "pg_restore", "age", "age-keygen", "bwrap"} {
		if _, err := os.Stat("/usr/bin/" + tool); err != nil {
			t.Fatalf("required local tool unavailable: %s", tool)
		}
	}
	root, err := os.MkdirTemp("/home/lightjunction/Backups", ".schema-backup-native-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if os.RemoveAll(root) != nil {
			t.Error("fixture backup cleanup failed")
		}
	})
	local := t.TempDir()
	if os.Chmod(local, 0700) != nil {
		t.Fatal("private fixture root")
	}
	data := filepath.Join(local, "pgdata")
	sock := "@lmm-schema-source-" + controllerBackupDigest([]byte(local))[:12]
	role := "lmm_schema_backup_fixture"
	env := []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C", "HOME=" + local, "TMPDIR=" + local, "PGHOST=" + sock, "PGPORT=5432", "PGUSER=" + role, "PGDATABASE=postgres"}
	run := func(tool string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/"+tool, args...)
		cmd.Env = env
		cmd.Dir = local
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("owned %s fixture failed: %v %s", tool, err, out)
		}
		return out
	}
	run("initdb", "-D", data, "-U", role, "--no-locale", "--encoding=UTF8", "--auth-local=trust", "--auth-host=reject")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/pg_ctl", "-D", data, "-m", "immediate", "-w", "stop")
		cmd.Env = env
		cmd.Dir = local
		if cmd.Run() != nil {
			t.Error("fixture source PostgreSQL shutdown failed")
		}
	})
	run("pg_ctl", "-D", data, "-l", filepath.Join(local, "postgres.log"), "-o", "-c listen_addresses= -c unix_socket_directories="+sock+" -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off", "-w", "start")
	sql := func(query string) []byte {
		return run("psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", query)
	}
	sql("CREATE SCHEMA app; CREATE ROLE fixture_reader NOLOGIN; GRANT USAGE ON SCHEMA app TO fixture_reader; CREATE TABLE app.credits(id bigserial PRIMARY KEY,amount bigint NOT NULL CHECK(amount>=0), payload text, stamp timestamptz NOT NULL DEFAULT now()); INSERT INTO app.credits(amount,payload) SELECT n::bigint*500000,md5(n::text) FROM generate_series(1,5000)n; GRANT SELECT ON app.credits TO fixture_reader; CREATE INDEX credits_payload ON app.credits(payload);")
	query, err := schemaMaintenanceBackupSourceQuery("app")
	if err != nil {
		t.Fatal(err)
	}
	source, err := decodeSchemaMaintenanceBackupSource(bytes.TrimSpace(sql(query)))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := decodeExistingSchemaSnapshot(sql(existingSchemaMetadataQuery("app")))
	if err != nil {
		t.Fatal(err)
	}
	_, plan, _ := newSchemaMaintenancePlanFixture(t)
	plan.PreSchema = contract
	plan.Role = role
	plan.BackupPolicy.ControllerDirectory = root
	plan.BackupPolicy.RestoreProofPath = filepath.Join(root, "restore-proof.json")
	if err = os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	plan.BackupPolicy.ControllerPublicKey, err = initializeControllerBackupKey(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(local, "age.key")
	run("age-keygen", "-o", identity)
	recipient := run("age-keygen", "-y", identity)
	plan.BackupPolicy.RecipientSHA256 = controllerBackupDigest(recipient)
	if err = os.WriteFile(filepath.Join(root, "age-recipient.txt"), recipient, 0600); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 64)
	now := time.Now().UTC().Truncate(time.Second)
	captures := schemaBackupTestStops(plan, sha, now)
	stops, err := schemaMaintenanceBackupStopBindings(plan, sha, captures)
	if err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(local, "database.pgc")
	run("pg_dump", "--format=custom", "--file", dump, "postgres")
	plain, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(content []byte) {
		t.Helper()
		if err = os.WriteFile(dump, content, 0600); err != nil {
			t.Fatal(err)
		}
		cipher := filepath.Join(root, "database.age")
		if err = os.Remove(cipher); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		run("age", "-r", strings.TrimSpace(string(recipient)), "-o", cipher, dump)
		if os.Chmod(cipher, 0600) != nil {
			t.Fatal("cipher private mode")
		}
		raw, err := os.ReadFile(cipher)
		if err != nil {
			t.Fatal(err)
		}
		set := productionSchemaMaintenanceBackupSet{Format: schemaMaintenanceBackupSetFormat, TransitionID: plan.TransitionID, PrePlanSHA256: sha, PreSchema: contract, CapturedUTC: now, RecipientSHA256: plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, SourceBefore: source, SourceAfter: source, Archive: controllerBackupArchive{CiphertextSHA256: controllerBackupDigest(raw), PlaintextSHA256: controllerBackupDigest(content), CiphertextBytes: int64(len(raw)), PlaintextBytes: int64(len(content))}}
		manifest, _ := json.MarshalIndent(set, "", "  ")
		manifest = append(manifest, '\n')
		if os.WriteFile(filepath.Join(root, "schema-backup-set.json"), manifest, 0600) != nil || os.WriteFile(filepath.Join(root, ".complete"), []byte(controllerBackupDigest(manifest)+"\n"), 0600) != nil {
			t.Fatal("fixture manifest write")
		}
	}
	verify := func(want bool) {
		t.Helper()
		runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: func() time.Time { return time.Now().UTC() }}
		raw, err := runtime.prepareSchemaMaintenanceBackup(context.Background(), plan, sha, captures, identity)
		if (err == nil) != want {
			t.Fatalf("actual isolated restore admitted=%t expected=%t err=%v", err == nil, want, err)
		}
		if want {
			proof, err := schemaMaintenanceBackupDecodeProof(raw, plan.BackupPolicy.ControllerPublicKey)
			if err != nil || !proof.ActualRestore || !proof.NoTCP || proof.TotalRows != 5000 || proof.TableCount != 1 {
				t.Fatal("actual restored content proof incomplete")
			}
		}
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".restore-private-") {
				t.Fatal("plaintext restore scratch leaked")
			}
		}
	}
	encode(plain)
	verify(true)
	truncated := plain[:len(plain)/2]
	encode(truncated)
	// A valid TOC is weaker than a completed restore of all data and indexes.
	run("pg_restore", "--list", dump)
	verify(false)
	sql("TRUNCATE app.credits;")
	run("pg_dump", "--format=custom", "--file", dump, "postgres")
	empty, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	encode(empty)
	verify(false)
	// Database SQL executing as the isolated superuser cannot replace the fixed
	// input query subsequently used to qualify the restored content.
	sql(`CREATE FUNCTION app.overwrite_snapshot() RETURNS boolean LANGUAGE plpgsql AS $f$ BEGIN IF current_database()='schema_restore' THEN EXECUTE 'COPY (SELECT 1) TO PROGRAM ''printf forged > /work/snapshot.sql'''; END IF; RETURN true; END $f$; INSERT INTO app.credits(amount,payload) VALUES(1,'synthetic'); ALTER TABLE app.credits ADD CONSTRAINT force_restore_write CHECK(app.overwrite_snapshot());`)
	run("pg_dump", "--format=custom", "--file", dump, "postgres")
	malicious, e := os.ReadFile(dump)
	if e != nil {
		t.Fatal(e)
	}
	encode(malicious)
	verify(false)
	attackScratch := filepath.Join(local, "attack-restore")
	if os.Mkdir(attackScratch, 0700) != nil {
		t.Fatal("attack scratch")
	}
	for name, content := range map[string][]byte{"identity": []byte("removed before sandbox"), "database.pgc": malicious, "roles.sql": []byte("CREATE ROLE fixture_reader NOLOGIN;\n"), "snapshot.sql": []byte("SELECT 1;\n")} {
		if os.WriteFile(filepath.Join(attackScratch, name), content, 0600) != nil {
			t.Fatal("attack scratch input")
		}
	}
	if runSchemaMaintenanceBackupSandbox(context.Background(), attackScratch, role) == nil {
		t.Fatal("dump COPY PROGRAM overwrote the qualification query")
	}
	attackLog, e := os.ReadFile(filepath.Join(attackScratch, "restore.log"))
	serverAttackLog, se := os.ReadFile(filepath.Join(attackScratch, "postgres.log"))
	protected, pe := os.ReadFile(filepath.Join(attackScratch, "snapshot.sql"))
	if e != nil || se != nil || pe != nil || !bytes.Equal(protected, []byte("SELECT 1;\n")) || !bytes.Contains(attackLog, []byte("program")) || !bytes.Contains(serverAttackLog, []byte("Read-only file system")) {
		t.Fatalf("fixed restore query protection was not exercised: %v %v %v fixed=%q restore=%s server=%s", e, se, pe, protected, attackLog, serverAttackLog)
	}
	sql("CREATE TYPE app.unsupported_enum AS ENUM('fixture');")
	if _, e := decodeSchemaMaintenanceBackupSource(bytes.TrimSpace(sql(query))); e == nil {
		t.Fatal("unsupported custom type silently qualified")
	}
	sql("DROP TYPE app.unsupported_enum; CREATE TABLE app.options(key text PRIMARY KEY,value text); INSERT INTO app.options VALUES('BusinessOption','unchanged'); CREATE TABLE app.referral_rewards(id bigint PRIMARY KEY,top_up_id bigint NOT NULL); INSERT INTO app.referral_rewards VALUES(1,1);")
	projectionBefore, e := decodeSchemaMaintenanceBackupSource(bytes.TrimSpace(sql(query)))
	if e != nil {
		t.Fatal(e)
	}
	originalBytes, _ := json.Marshal(projectionBefore)
	_ = schemaMaintenanceBackupLogicalSHA(projectionBefore)
	if !schemaMaintenanceBackupLogicalEqual(projectionBefore, projectionBefore) {
		t.Fatal("logical comparison not reflexive")
	}
	afterHashBytes, _ := json.Marshal(projectionBefore)
	if !bytes.Equal(originalBytes, afterHashBytes) {
		t.Fatal("logical normalization mutated physical source inventory")
	}
	owner := deploymentfence.OwnerBinding{Format: 1, State: "ACTIVE", DeploymentID: "schema-projection-fixture", Host: "arch-dmit", PlanSHA256: sha, ContractSHA256: sha, ProviderSHA256: sha, Nonce: strings.Repeat("1", 32), HolderPID: 101, HolderUnit: "lmm-schema-maintenance-schema-projection-fixture.service", HolderInvocationID: strings.Repeat("2", 32), BackendPID: 202, SystemIdentifier: projectionBefore.SystemIdentifier, Database: projectionBefore.Database, DatabaseOID: projectionBefore.DatabaseOID, Schema: projectionBefore.Schema, SchemaOID: projectionBefore.SchemaOID, Role: role, Purpose: "schema-maintenance", Service: "lmm-api.service"}
	ownerBytes, _ := json.Marshal(owner)
	ownerKey := deploymentfence.OptionPrefix + owner.DeploymentID + ":" + owner.Host
	projectedQuery, e := schemaMaintenanceBackupProjectedSourceQuery("app", projectionBefore, ownerBytes)
	if e != nil {
		t.Fatal(e)
	}
	sql("ALTER TABLE app.credits ADD COLUMN additive text DEFAULT 'new'; ALTER TABLE app.referral_rewards ALTER COLUMN top_up_id DROP NOT NULL; CREATE TABLE app.new_feature(id bigint); INSERT INTO app.options VALUES(" + schemaMaintenanceBackupLiteral(ownerKey) + "," + schemaMaintenanceBackupLiteral(string(ownerBytes)) + ");")
	project := func() productionSchemaMaintenanceBackupSource {
		t.Helper()
		p, e := decodeSchemaMaintenanceBackupProjectedSource(bytes.TrimSpace(sql(projectedQuery)))
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	if e := validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()); e != nil {
		t.Fatal("additive DDL and sole control-plane owner changed business projection:", e)
	}
	sql("INSERT INTO app.options VALUES('MerchantStoreDeploymentFence:unknown:host','ACTIVE');")
	if validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()) == nil {
		t.Fatal("unknown owner hidden by projection")
	}
	sql("DELETE FROM app.options WHERE key='MerchantStoreDeploymentFence:unknown:host'; UPDATE app.options SET value='changed' WHERE key=" + schemaMaintenanceBackupLiteral(ownerKey) + ";")
	if validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()) == nil {
		t.Fatal("changed self owner hidden by projection")
	}
	sql("UPDATE app.options SET value=NULL WHERE key=" + schemaMaintenanceBackupLiteral(ownerKey) + ";")
	if validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()) == nil {
		t.Fatal("NULL self owner hidden by SQL unknown predicate")
	}
	sql("UPDATE app.options SET value=" + schemaMaintenanceBackupLiteral(string(ownerBytes)) + " WHERE key=" + schemaMaintenanceBackupLiteral(ownerKey) + ";")
	sql("UPDATE app.options SET value='changed' WHERE key='BusinessOption';")
	if validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()) == nil {
		t.Fatal("business option change hidden by control-plane exclusion")
	}
	sql("UPDATE app.options SET value='unchanged' WHERE key='BusinessOption'; ALTER TABLE app.credits ALTER COLUMN amount DROP NOT NULL;")
	if validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()) == nil {
		t.Fatal("unapproved old column semantics changed")
	}
	sql("ALTER TABLE app.credits ALTER COLUMN amount SET NOT NULL; DROP INDEX app.credits_payload; CREATE INDEX credits_payload ON app.credits(payload);")
	if validateSchemaMaintenanceBackupProjectedEqual(projectionBefore, project()) == nil {
		t.Fatal("old index drop and recreation accepted")
	}
	t.Logf("actual source snapshot bytes=%d; tables=%d", len(originalBytes), len(projectionBefore.Tables))
	sql(`DO $f$ BEGIN FOR n IN 1..190 LOOP EXECUTE format('CREATE TABLE app.scale_%s(id bigint PRIMARY KEY,payload text,stamp timestamptz DEFAULT now())',lpad(n::text,3,'0')); END LOOP; END $f$;`)
	scaleBytes := bytes.TrimSpace(sql(query))
	scale, e := decodeSchemaMaintenanceBackupSource(scaleBytes)
	if e != nil || len(scale.Tables) != 194 {
		t.Fatalf("194-table snapshot bounded inventory: tables=%d err=%v", len(scale.Tables), e)
	}
	t.Logf("actual synthetic 194-table source bytes=%d; bounded source=%d set=%d", len(scaleBytes), schemaMaintenanceBackupSourceMaxBytes, schemaMaintenanceBackupSetMaxBytes)
}
