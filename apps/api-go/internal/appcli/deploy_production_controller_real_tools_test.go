//go:build linux

package appcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt in explicitly: this creates only an owned, temporary PostgreSQL cluster
// with no TCP listener, and never uses the workstation's database or credentials.
func TestControllerBackupImportWithRealAgeAndPostgres(t *testing.T) {
	if os.Getenv("LMM_CONTROLLER_BACKUP_REAL_TOOLS_TEST") != "1" {
		t.Skip("requires explicit local real-tool test opt-in")
	}
	for _, tool := range []string{"age", "age-keygen", "initdb", "pg_ctl", "pg_dump", "pg_restore", "psql"} {
		if _, err := os.Stat("/usr/bin/" + tool); err != nil {
			t.Fatalf("required local test tool unavailable: %s", tool)
		}
	}
	fixture := newControllerImportFixture(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "pgdata")
	socket := "@lmm-import-" + controllerBackupDigest([]byte(root))[:16]
	env := []string{"PATH=/usr/bin", "LANG=C", "LC_ALL=C", "HOME=" + root, "TMPDIR=" + root,
		"PGHOST=" + socket, "PGPORT=55432", "PGUSER=lmm_import_fixture", "PGDATABASE=postgres"}
	run := func(tool string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/"+tool, args...)
		cmd.Env, cmd.Dir = env, root
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("owned local %s fixture failed: %v", tool, err)
		}
		return output
	}
	run("initdb", "-D", data, "--no-locale", "--encoding=UTF8", "--auth=trust", "--username=lmm_import_fixture")
	// Register stop before start: even a startup timeout leaves a scoped cleanup.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/pg_ctl", "-D", data, "-m", "immediate", "-w", "stop")
		cmd.Env, cmd.Dir = env, root
		if err := cmd.Run(); err != nil {
			t.Errorf("owned temporary PostgreSQL stop failed: %v", err)
		}
	})
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-o", "-h '' -k "+socket+" -p 55432 -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off", "start")
	run("psql", "--no-psqlrc", "--set", "ON_ERROR_STOP=1", "-c", "CREATE TABLE backup_fixture AS SELECT n, md5(n::text) AS value FROM generate_series(1, 5000) AS n")
	dump := filepath.Join(root, "database.pgc")
	run("pg_dump", "--format=custom", "--compress=zstd:1", "--file", dump, "postgres")
	if err := os.Remove(fixture.identity); err != nil {
		t.Fatal(err)
	}
	run("age-keygen", "-o", fixture.identity)
	recipient := strings.TrimSpace(string(run("age-keygen", "-y", fixture.identity)))
	plaintexts := make(map[string][]byte)
	for _, kind := range controllerBackupKinds {
		archive := fixture.set.Archives[kind]
		plain := fixture.runner.plain[archive.CiphertextSHA256]
		if kind == "database" {
			var err error
			plain, err = os.ReadFile(dump)
			if err != nil {
				t.Fatal(err)
			}
		}
		plaintexts[kind] = plain
	}
	encrypt := func(kind string, plaintext []byte) {
		t.Helper()
		archive := fixture.set.Archives[kind]
		plain := filepath.Join(root, kind+".plain")
		if err := os.WriteFile(plain, plaintext, 0o600); err != nil {
			t.Fatal(err)
		}
		cipher := filepath.Join(fixture.root, kind+".age")
		if err := os.Remove(cipher); err != nil {
			t.Fatal(err)
		}
		run("age", "-r", recipient, "-o", cipher, plain)
		if err := os.Chmod(cipher, 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(cipher)
		if err != nil {
			t.Fatal(err)
		}
		archive.CiphertextBytes, archive.PlaintextBytes = info.Size(), int64(len(plaintext))
		archive.CiphertextSHA256, archive.PlaintextSHA256 = mustHashFile(t, cipher), controllerBackupDigest(plaintext)
		fixture.set.Archives[kind] = archive
		if err := os.Remove(plain); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range controllerBackupKinds {
		encrypt(kind, plaintexts[kind])
	}
	fixture.manifest(nil)
	fixture.runtime.runner = osProductionCommandRunner{}
	if _, _, err := fixture.verify(); err != nil {
		t.Fatal("real age/PostgreSQL import rejected valid complete archive")
	}
	// Keep age encryption and both declared hashes valid: only the full custom
	// dump parser can reject this truncated PostgreSQL payload.
	encrypt("database", plaintexts["database"][:len(plaintexts["database"])/2])
	fixture.manifest(nil)
	if _, _, err := fixture.verify(); err == nil {
		t.Fatal("real PostgreSQL parser accepted a truncated archive")
	}
	encrypt("database", plaintexts["database"])
	archive := fixture.set.Archives["application"]
	cipherPath := filepath.Join(fixture.root, "application.age")
	cipher, err := os.ReadFile(cipherPath)
	if err != nil {
		t.Fatal(err)
	}
	cipher[len(cipher)-1] ^= 1
	if err := os.WriteFile(cipherPath, cipher, 0o600); err != nil {
		t.Fatal(err)
	}
	archive.CiphertextSHA256 = controllerBackupDigest(cipher)
	fixture.set.Archives["application"] = archive
	fixture.manifest(nil)
	if _, _, err := fixture.verify(); err == nil {
		t.Fatal("real age accepted damaged authenticated EOF with self-consistent ciphertext hash")
	}
}
