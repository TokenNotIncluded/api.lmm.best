package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func schemaBackupCollectFixturePlain() []byte {
	return append([]byte("PGDMP"), bytes.Repeat([]byte{0, 1, 2, 255}, 65536)...)
}
func TestSchemaMaintenanceBackupCollectSubprocess(t *testing.T) {
	mode := os.Getenv("LMM_SCHEMA_BACKUP_COLLECT_TEST_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "dump":
		_, _ = os.Stdout.Write(schemaBackupCollectFixturePlain())
	case "bad-magic":
		_, _ = os.Stdout.Write([]byte("not-a-custom-dump"))
	case "dump-error":
		_, _ = os.Stderr.WriteString("SECRET_FIXTURE_CONNECTION_PASSWORD")
		os.Exit(23)
	case "encrypt-error":
		_, _ = os.Stderr.WriteString("SECRET_FIXTURE_CONNECTION_PASSWORD")
		os.Exit(24)
	case "stall-encrypt":
		time.Sleep(time.Hour)
	case "blocked-dump":
		for {
			if _, err := os.Stdout.Write(bytes.Repeat([]byte("PGDMP"), 1<<16)); err != nil {
				os.Exit(25)
			}
		}
	default:
		os.Exit(26)
	}
	os.Exit(0)
}
func schemaBackupCollectProcess(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSchemaMaintenanceBackupCollectSubprocess$")
	cmd.Env = append(os.Environ(), "LMM_SCHEMA_BACKUP_COLLECT_TEST_HELPER="+mode)
	return cmd
}
func TestSchemaMaintenanceBackupCollectPipeEncryptsAndHashesExactStream(t *testing.T) {
	if _, err := os.Stat("/usr/bin/age"); err != nil {
		t.Skip("age unavailable")
	}
	root := t.TempDir()
	key := filepath.Join(root, "identity")
	if out, err := exec.Command("/usr/bin/age-keygen", "--output", key).CombinedOutput(); err != nil {
		t.Fatalf("fixture key generation failed: %v", err)
	} else {
		_ = out
	}
	public, err := exec.Command("/usr/bin/age-keygen", "-y", key).Output()
	if err != nil {
		t.Fatal(err)
	}
	recipient := strings.TrimSpace(string(public))
	if !schemaMaintenanceBackupRecipient(recipient) {
		t.Fatal("real age recipient refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cipher := newSchemaMaintenanceBackupHashWriter(&bytes.Buffer{})
	buffer := &bytes.Buffer{}
	cipher.destination = buffer
	dump := schemaBackupCollectProcess(ctx, "dump")
	encrypt := exec.CommandContext(ctx, "/usr/bin/age", "--encrypt", "--recipient", recipient)
	sha, size, err := runSchemaMaintenanceBackupPipe(ctx, dump, encrypt, cipher)
	plain := schemaBackupCollectFixturePlain()
	if err != nil || sha != controllerBackupDigest(plain) || size != int64(len(plain)) {
		t.Fatalf("exact streaming plaintext digest missing: %v", err)
	}
	if cipher.bytes <= size || bytes.Contains(buffer.Bytes(), plain[:30]) || !bytes.HasPrefix(buffer.Bytes(), []byte("age-encryption.org/v1\n")) {
		t.Fatal("destination is not age ciphertext")
	}
	decrypt := exec.CommandContext(ctx, "/usr/bin/age", "--decrypt", "--identity", key)
	decrypt.Stdin = bytes.NewReader(buffer.Bytes())
	restored, err := decrypt.Output()
	if err != nil || !bytes.Equal(restored, plain) {
		t.Fatal("actual age decrypt did not reproduce the streamed dump")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "identity" {
		t.Fatal("plaintext or extra output reached fixture disk")
	}
}
func TestSchemaMaintenanceBackupCollectPipeFailuresAreBoundedAndRedacted(t *testing.T) {
	for _, mode := range []string{"dump-error", "bad-magic", "encrypt-error", "context-cancel", "live-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			dumpMode := "dump"
			if mode == "dump-error" || mode == "bad-magic" {
				dumpMode = mode
			}
			if mode == "context-cancel" {
				dumpMode = "blocked-dump"
				cancel()
			}
			if mode == "live-cancel" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				dumpMode = "blocked-dump"
			}
			dump := schemaBackupCollectProcess(ctx, dumpMode)
			// cat stands in for an accepting encryption sink in failure-only fixtures.
			age := exec.CommandContext(ctx, "/usr/bin/cat")
			if mode == "encrypt-error" {
				dump = schemaBackupCollectProcess(ctx, "blocked-dump")
				age = schemaBackupCollectProcess(ctx, "encrypt-error")
			}
			if mode == "live-cancel" {
				age = schemaBackupCollectProcess(ctx, "stall-encrypt")
			}
			start := time.Now()
			_, _, err := runSchemaMaintenanceBackupPipe(ctx, dump, age, io.Discard)
			if err == nil || strings.Contains(err.Error(), "SECRET_") || time.Since(start) > 3*time.Second {
				t.Fatalf("failed stream was not bounded/redacted: %v", err)
			}
		})
	}
}
func TestSchemaMaintenanceBackupCollectWriterRejectsLimitAndShortWrite(t *testing.T) {
	writer := newSchemaMaintenanceBackupHashWriter(io.Discard)
	writer.bytes = controllerBackupMaxArchive - 3
	if _, err := writer.Write([]byte("PGDMP")); err == nil || writer.bytes != controllerBackupMaxArchive-3 {
		t.Fatal("oversize stream accepted or digest mutated")
	}
	writer = newSchemaMaintenanceBackupHashWriter(schemaBackupCollectShortWriter{})
	if _, err := writer.Write([]byte("PGDMP")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short destination silently accepted")
	}
	for _, recipient := range []string{"", "ssh-ed25519 AAAA", "age1" + strings.Repeat("a", 58) + "\n", "age1" + strings.Repeat("b", 58)} {
		if schemaMaintenanceBackupRecipient(recipient) {
			t.Fatal("unsupported recipient accepted")
		}
	}
}

type schemaBackupCollectShortWriter struct{}

func (schemaBackupCollectShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

// All connection and transport behavior below is injected. The fixture writes
// only synthetic encrypted bytes beneath its own marker-free temporary backup
// directory; it never reaches SSH or an application database.
type schemaBackupCollectImportRunner struct {
	t             *testing.T
	reply         []byte
	cipher        []byte
	commands      []productionCommand
	failSSH       bool
	corruptCipher bool
}

func (r *schemaBackupCollectImportRunner) Run(_ context.Context, cmd productionCommand) ([]byte, error) {
	r.commands = append(r.commands, cmd)
	strict := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10"}
	if len(cmd.Args) < len(strict) || !reflect.DeepEqual(cmd.Args[:len(strict)], strict) {
		r.t.Fatal("transport relaxed strict host keys or process isolation")
	}
	switch cmd.Name {
	case commandSSH:
		if cmd.Args[len(strict)] != "ArchDmit" || !strings.Contains(cmd.Args[len(cmd.Args)-1], "'--action' 'backup-export'") || cmd.Timeout < 15*time.Minute {
			r.t.Fatal("not the fixed primary native export")
		}
		if r.failSSH {
			return nil, errors.New("unknown accepted export")
		}
		return r.reply, nil
	case commandSCP:
		args := cmd.Args[len(strict):]
		if len(args) != 3 || args[0] != "--" || !strings.HasPrefix(args[1], "ArchDmit:") || !strings.HasSuffix(args[1], "/state/schema-database.age") {
			r.t.Fatal("ciphertext source was not fixed")
		}
		raw := r.cipher
		if r.corruptCipher {
			raw = append([]byte(nil), raw...)
			raw[0] ^= 1
		}
		return nil, os.WriteFile(args[2], raw, 0600)
	default:
		r.t.Fatal("unexpected command")
		return nil, errors.New("unexpected command")
	}
}
func schemaBackupCollectImportFixture(t *testing.T) (*productionReleaseRuntime, productionSchemaMaintenancePlan, string, []productionSchemaMaintenanceHostCapture, *schemaBackupCollectImportRunner) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("controller fixture requires ordinary UID")
	}
	root, err := os.MkdirTemp("/home/lightjunction/Backups", ".schema-collect-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private backup directory unavailable")
	}
	fixtureRuntime, plan, _ := newSchemaMaintenancePlanFixture(t)
	// Rebase only synthetic canonical path declarations to actual fixed host
	// roots. No host-side paths below are created or accessed by this fixture.
	raw, _ := json.Marshal(plan)
	fixedWorkRoot := defaultProductionPaths().WorkRoot
	raw = bytes.ReplaceAll(raw, []byte(fixtureRuntime.paths.WorkRoot), []byte(fixedWorkRoot))
	// Schema and capsule roots derive from the WorkRoot parent, not WorkRoot.
	raw = bytes.ReplaceAll(raw, []byte(filepath.Dir(fixtureRuntime.paths.WorkRoot)), []byte(filepath.Dir(fixedWorkRoot)))
	if json.Unmarshal(raw, &plan) != nil {
		t.Fatal("fixture plan rebase failed")
	}
	plan.BackupPolicy.ControllerDirectory = root
	plan.BackupPolicy.RestoreProofPath = filepath.Join(root, "restore-proof.json")
	recipient := []byte("age1" + strings.Repeat("a", 58) + "\n")
	plan.BackupPolicy.RecipientSHA256 = controllerBackupDigest(recipient)
	if os.WriteFile(filepath.Join(root, "age-recipient.txt"), recipient, 0600) != nil {
		t.Fatal("public recipient unavailable")
	}
	canonical, err := canonicalProductionSchemaMaintenancePlan(plan, defaultProductionPaths())
	if err != nil {
		t.Fatal(err)
	}
	sha := controllerBackupDigest(canonical)
	now := time.Now().UTC().Truncate(time.Second)
	captures := schemaBackupTestStops(plan, sha, now)
	stops, err := schemaMaintenanceBackupStopBindings(plan, sha, captures)
	if err != nil {
		t.Fatal(err)
	}
	source := productionSchemaMaintenanceBackupSource{Format: 1, TransactionReadOnly: "on", SystemIdentifier: plan.PreSchema.SystemIdentifier, Database: plan.PreSchema.Database, DatabaseOID: plan.PreSchema.DatabaseOID, Schema: plan.PreSchema.Schema, SchemaOID: plan.PreSchema.SchemaOID, Roles: []string{plan.Role}, Sequences: []productionSchemaMaintenanceBackupSequence{}, Indexes: []productionSchemaMaintenanceBackupIndex{}, StructureSHA256: strings.Repeat("d", 64), Tables: []productionSchemaMaintenanceBackupTable{{Name: "synthetic", TableOID: 100, ColumnNames: []string{"id"}, Columns: []productionSchemaMaintenanceBackupColumn{{Position: 1, Name: "id", TypeName: "bigint", TypeOID: 20, TypeMod: -1}}, Rows: 1, SHA256: strings.Repeat("e", 64), ProjectedRows: 1, ProjectedSHA256: strings.Repeat("e", 64)}}}
	cipher := []byte("age-encryption.org/v1\nsynthetic-fixture-ciphertext-only")
	set := productionSchemaMaintenanceBackupSet{Format: schemaMaintenanceBackupSetFormat, TransitionID: plan.TransitionID, PrePlanSHA256: sha, PreSchema: plan.PreSchema, CapturedUTC: now, RecipientSHA256: plan.BackupPolicy.RecipientSHA256, StoppedOrigins: stops, SourceBefore: source, SourceAfter: source, Archive: controllerBackupArchive{CiphertextSHA256: controllerBackupDigest(cipher), PlaintextSHA256: strings.Repeat("f", 64), CiphertextBytes: int64(len(cipher)), PlaintextBytes: 10}}
	result, _ := json.Marshal(set)
	reply := schemaMaintenanceRPCReply{Protocol: schemaMaintenanceRPCProtocol, Binding: schemaMaintenanceRPCBinding{PlanSHA256: sha, TransitionID: plan.TransitionID, DaemonPID: 222, DaemonInvocationID: strings.Repeat("1", 32), DaemonExeSHA256: plan.Worker.Provider.DeployEngineSHA256, DaemonStartTicks: 123}, Action: "backup-export", Nonce: strings.Repeat("2", 64), OK: true, Result: result}
	replyRaw, err := schemaMaintenanceRPCMarshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	runner := &schemaBackupCollectImportRunner{t: t, reply: append(replyRaw, '\n'), cipher: cipher}
	return &productionReleaseRuntime{runner: runner, now: func() time.Time { return now }}, plan, sha, captures, runner
}
func TestSchemaMaintenanceBackupCollectControllerImportSealsExactBytesAndNeverReexports(t *testing.T) {
	runtime, plan, sha, captures, runner := schemaBackupCollectImportFixture(t)
	var diagnosticReply schemaMaintenanceRPCReply
	if err := schemaMaintenanceRPCDecode(bytes.TrimSuffix(runner.reply, []byte{'\n'}), &diagnosticReply); err != nil {
		t.Fatalf("fixture RPC: %v", err)
	}
	var diagnosticSet productionSchemaMaintenanceBackupSet
	if err := decodeControllerBackupJSON(diagnosticReply.Result, &diagnosticSet); err != nil {
		t.Fatalf("fixture set decode: %v [null at %d]", err, bytes.Index(diagnosticReply.Result, []byte("null")))
	}
	diagnosticStops, _ := schemaMaintenanceBackupStopBindings(plan, sha, captures)
	if err := validateSchemaMaintenanceBackupSet(diagnosticSet, plan, sha, diagnosticStops, runtime.now()); err != nil {
		t.Fatalf("fixture set: %v", err)
	}
	if err := controllerBackupCheckSpace(plan.BackupPolicy.ControllerDirectory, diagnosticSet.Archive.CiphertextBytes); err != nil {
		t.Fatalf("fixture space: %v", err)
	}
	set, err := runtime.collectSchemaMaintenanceBackup(context.Background(), plan, sha, captures)
	if err != nil {
		t.Fatalf("%v (commands=%d)", err, len(runner.commands))
	}
	root := plan.BackupPolicy.ControllerDirectory
	cipher, err := controllerBackupReadPrivate(filepath.Join(root, "database.age"), os.Geteuid(), 1000)
	if err != nil || !bytes.Equal(cipher, runner.cipher) {
		t.Fatal("cipher import changed bytes")
	}
	raw, err := controllerBackupReadPrivate(filepath.Join(root, schemaMaintenanceBackupCollectionName), os.Geteuid(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.MarshalIndent(set, "", "  ")
	if !bytes.Equal(raw, append(expected, '\n')) {
		t.Fatal("manifest not exact canonical export")
	}
	complete, err := controllerBackupReadPrivate(filepath.Join(root, ".complete"), os.Geteuid(), 65)
	if err != nil || string(complete) != controllerBackupDigest(raw)+"\n" {
		t.Fatal("completion does not bind exact import")
	}
	if _, err = runtime.collectSchemaMaintenanceBackup(context.Background(), plan, sha, captures); err == nil || len(runner.commands) != 2 {
		t.Fatal("existing collected backup was dispatched again")
	}
}
func TestSchemaMaintenanceBackupCollectControllerRejectsUnknownAndChangedBytes(t *testing.T) {
	for _, mode := range []string{"unknown-export", "changed-cipher", "other-plan", "other-origin", "ambiguous-result"} {
		t.Run(mode, func(t *testing.T) {
			runtime, plan, sha, captures, runner := schemaBackupCollectImportFixture(t)
			switch mode {
			case "unknown-export":
				runner.failSSH = true
			case "changed-cipher":
				runner.corruptCipher = true
			case "other-plan":
				sha = strings.Repeat("0", 64)
			case "other-origin":
				captures[1].InvocationID = strings.Repeat("9", 32)
			case "ambiguous-result":
				runner.reply = bytes.Replace(runner.reply, []byte("\"captured_utc\""), []byte("\"CAPTURED_UTC\""), 1)
			}
			if _, err := runtime.collectSchemaMaintenanceBackup(context.Background(), plan, sha, captures); err == nil {
				t.Fatal("unsafe import accepted")
			}
			if _, err := os.Lstat(filepath.Join(plan.BackupPolicy.ControllerDirectory, ".complete")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed import sealed as complete")
			}
			if mode == "unknown-export" {
				if _, err := runtime.collectSchemaMaintenanceBackup(context.Background(), plan, sha, captures); err == nil || len(runner.commands) != 1 {
					t.Fatal("unknown native export retried")
				}
			}
			if mode == "other-plan" && len(runner.commands) != 0 {
				t.Fatal("another pre-plan was sent to native daemon")
			}

		})
	}
}
func TestSchemaMaintenanceBackupCollectRefusesMissingPrimaryAuthority(t *testing.T) {
	for _, controller := range []*productionSchemaMaintenanceController{nil, {}, {runtime: &productionRuntime{}}} {
		if _, err := controller.collectBackup(context.Background()); err == nil {
			t.Fatal("missing original daemon authority accepted")
		}
	}
}

// Explicit opt-in. Both databases live in a newly owned no-TCP fixture cluster;
// only synthetic rows are created. This exercises the actual fixed pg_dump/age
// runner and restores its decrypted in-memory archive, never a production DSN.
func TestSchemaMaintenanceBackupCollectActualReadOnlyPGDumpAgeStream(t *testing.T) {
	if os.Getenv("LMM_SCHEMA_BACKUP_COLLECT_REAL_TOOLS_TEST") != "1" {
		t.Skip("requires explicit owned local PostgreSQL/age test opt-in")
	}
	if os.Geteuid() == 0 {
		t.Fatal("real streaming fixture requires ordinary UID")
	}
	for _, tool := range []string{"initdb", "pg_ctl", "psql", "pg_dump", "pg_restore", "age", "age-keygen"} {
		if _, err := os.Stat("/usr/bin/" + tool); err != nil {
			t.Fatalf("fixture tool unavailable: %s", tool)
		}
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture root unavailable")
	}
	socket := filepath.Join(root, "socket")
	if os.Mkdir(socket, 0700) != nil {
		t.Fatal("private socket unavailable")
	}
	data := filepath.Join(root, "pgdata")
	environment := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + root, "PGHOST=" + socket, "PGUSER=lmm_schema_collect_fixture", "PGDATABASE=postgres"}
	run := func(name string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/"+name, args...)
		cmd.Env = environment
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture %s failed: %v", name, err)
		}
		return out
	}
	run("initdb", "-D", data, "-U", "lmm_schema_collect_fixture", "--no-locale", "--encoding=UTF8", "--auth-local=trust", "--auth-host=reject")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-o", "-c listen_addresses= -c unix_socket_directories="+socket+" -c unix_socket_permissions=0700 -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off", "-w", "start")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/pg_ctl", "-D", data, "-m", "immediate", "-w", "stop")
		cmd.Env = environment
		if cmd.Run() != nil {
			t.Error("fixture PostgreSQL cleanup failed")
		}
	})
	if string(run("psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-c", "SELECT current_setting('listen_addresses')='' AND inet_server_addr() IS NULL")) != "t\n" {
		t.Fatal("fixture opened TCP")
	}
	run("psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-c", "CREATE SCHEMA fixture; CREATE TABLE fixture.ledger(id bigint PRIMARY KEY, amount numeric NOT NULL, payload bytea NOT NULL); INSERT INTO fixture.ledger VALUES (1,1.25,decode('00ff','hex')),(2,2.50,decode('fe00','hex'))")
	key := filepath.Join(root, "identity")
	run("age-keygen", "--output", key)
	recipient := strings.TrimSpace(string(run("age-keygen", "-y", key)))
	target := filepath.Join(root, "database.age")
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cipher := newSchemaMaintenanceBackupHashWriter(file)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	readOnlyEnvironment := append(append([]string(nil), environment...), "PGOPTIONS=-c search_path=fixture -c default_transaction_read_only=on")
	sha, size, err := (osProductionCommandRunner{}).StreamSchemaMaintenanceBackup(ctx, "postgresql:///postgres", readOnlyEnvironment, "fixture", recipient, cipher)
	closeErr := file.Close()
	if err != nil || closeErr != nil || size <= 0 || cipher.bytes <= size {
		t.Fatalf("actual fixed encrypted stream failed: %v", err)
	}
	archive, err := exec.CommandContext(ctx, "/usr/bin/age", "--decrypt", "--identity", key, target).Output()
	if err != nil || controllerBackupDigest(archive) != sha || int64(len(archive)) != size || !bytes.HasPrefix(archive, []byte("PGDMP")) {
		t.Fatal("actual encrypted stream archive digest mismatch")
	}
	run("psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-c", "CREATE DATABASE schema_collect_restore")
	restore := exec.CommandContext(ctx, "/usr/bin/pg_restore", "--format=custom", "--no-password", "--exit-on-error", "--single-transaction", "--dbname=schema_collect_restore")
	restore.Env = environment
	restore.Stdin = bytes.NewReader(archive)
	restore.Stdout, restore.Stderr = io.Discard, io.Discard
	if restore.Run() != nil {
		t.Fatal("actual streamed fixture archive failed full restore")
	}
	if string(run("psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "--dbname=schema_collect_restore", "-c", "SELECT count(*),sum(amount),bool_and(payload IS NOT NULL) FROM fixture.ledger")) != "2|3.75|t\n" {
		t.Fatal("restored synthetic rows changed")
	}
}

func TestSchemaMaintenanceBackupCollectDumpEnvironmentKeepsOnlySealedReadOnlyPG(t *testing.T) {
	inspected := []string{"SQL_DSN=postgres://fixture:secret@localhost/fixture", "SESSION_SECRET=secret", "LD_PRELOAD=/unsafe/library", "PATH=/unsafe/bin", "LANG=unsafe", "PGOPTIONS=-c search_path=fixture -c default_transaction_read_only=on", "PGPASSWORD=fixture-password", "PGSSLROOTCERT=/sealed/root.crt", "PGPASSFILE=/unsafe/pgpass", "PGSERVICEFILE=/unsafe/service"}
	child, err := schemaMaintenanceBackupDumpEnvironment(inspected, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]string{}
	for _, entry := range child {
		key, value, _ := strings.Cut(entry, "=")
		actual[key] = value
	}
	if actual["PGPASSWORD"] != "fixture-password" || actual["PGSSLROOTCERT"] != "/sealed/root.crt" || actual["PATH"] != "/usr/bin:/bin" || actual["LANG"] != "C" || actual["PGPASSFILE"] != "/dev/null" || actual["PGSERVICEFILE"] != "/dev/null" {
		t.Fatal("sealed PG authority or fixed child controls lost")
	}
	for _, key := range []string{"SQL_DSN", "SESSION_SECRET", "LD_PRELOAD"} {
		if _, exists := actual[key]; exists {
			t.Fatal("application or ambient authority leaked to dump")
		}
	}
	for _, bad := range [][]string{nil, {"PGOPTIONS=-c search_path=fixture -c default_transaction_read_only=off"}, {"PGOPTIONS=-c search_path=fixture -c default_transaction_read_only=on", "PGOPTIONS="}} {
		if _, err := schemaMaintenanceBackupDumpEnvironment(bad, "fixture"); err == nil {
			t.Fatal("missing, writable or duplicate read-only projection accepted")
		}
	}
}
