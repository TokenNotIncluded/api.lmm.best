//go:build linux

package appcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProductionProbeSelectionSkipsInternalAndOAuthTokensInPostgres(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("initdb requires an unprivileged test user")
	}
	for _, name := range []string{"initdb", "pg_ctl", "psql"} {
		if _, err := os.Stat("/usr/bin/" + name); err != nil {
			t.Skipf("local PostgreSQL tool %s is unavailable", name)
		}
	}

	root := t.TempDir()
	data := filepath.Join(root, "pgdata")
	socket := "@lmm-probe-" + controllerBackupDigest([]byte(root))[:16]
	const port = "55433"
	const user = "lmm_probe_fixture"
	env := []string{"PATH=/usr/bin", "LANG=C", "LC_ALL=C", "HOME=" + root,
		"PGHOST=" + socket, "PGPORT=" + port, "PGUSER=" + user, "PGDATABASE=postgres"}
	run := func(tool string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "/usr/bin/"+tool, args...)
		command.Env, command.Dir = env, root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("temporary PostgreSQL %s failed: %v: %s", tool, err, output)
		}
	}
	run("initdb", "-D", data, "--no-locale", "--encoding=UTF8", "--auth=trust", "--username="+user)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "/usr/bin/pg_ctl", "-D", data, "-m", "immediate", "-w", "stop")
		command.Env, command.Dir = env, root
		if err := command.Run(); err != nil {
			t.Errorf("temporary PostgreSQL stop failed: %v", err)
		}
	})
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w",
		"-o", "-h '' -k "+socket+" -p "+port+" -c shared_buffers=16MB -c max_connections=8 -c autovacuum=off", "start")

	const manual = "manual_test_01234567890123456789"
	run("psql", "-X", "-v", "ON_ERROR_STOP=1", "-c", `
CREATE TABLE users (id bigint PRIMARY KEY, status integer NOT NULL, role integer NOT NULL);
CREATE TABLE tokens (id bigint PRIMARY KEY, user_id bigint NOT NULL, key text NOT NULL,
  deleted_at timestamptz, oauth_managed boolean NOT NULL, creation_source text,
  status integer NOT NULL, expired_time bigint NOT NULL, unlimited_quota boolean NOT NULL,
  remain_quota bigint NOT NULL, allow_ips text);
INSERT INTO users VALUES (1, 1, 10);
INSERT INTO tokens VALUES
  (642, 1, 'assistant_runtime_test_0123456789', NULL, false, 'assistant_runtime', 1, -1, true, 1000000, ''),
  (700, 1, 'oauth_managed_test_012345678901', NULL, true, 'manual', 1, -1, true, 1000000, ''),
  (316, 1, '`+manual+`', NULL, false, 'manual', 1, -1, false, 100, '');`)

	t.Setenv("PGHOST", socket)
	t.Setenv("PGPORT", port)
	workspace := productionWorkspace{probeToken: filepath.Join(root, "probe-token")}
	runtime := &productionRuntime{runner: osProductionCommandRunner{}}
	schema, err := runtime.captureDatabaseAccess(context.Background(), workspace,
		[]byte("SQL_DSN=postgres://"+user+"@/postgres?sslmode=disable\n"))
	if err != nil || schema != "public" {
		t.Fatalf("eligible manual token was not selected: schema=%q err=%v", schema, err)
	}
	chosen, err := os.ReadFile(workspace.probeToken)
	if err != nil || string(chosen) != "sk-"+manual+"\n" {
		t.Fatal("probe selected an internal or OAuth-managed credential")
	}

	run("psql", "-X", "-v", "ON_ERROR_STOP=1", "-c", "DELETE FROM tokens WHERE id = 316")
	workspace.probeToken = filepath.Join(root, "no-eligible-token")
	if _, err := runtime.captureDatabaseAccess(context.Background(), workspace,
		[]byte("SQL_DSN=postgres://"+user+"@/postgres?sslmode=disable\n")); err == nil {
		t.Fatal("internal-only credentials were accepted for the production probe")
	}
	if _, err := os.Lstat(workspace.probeToken); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed selection left a credential file")
	}
}
