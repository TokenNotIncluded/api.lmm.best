package deploycli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchemaMaintenanceBackupSourceExternalTypeActualPrivatePostgres(t *testing.T) {
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for an owned local PostgreSQL cluster")
	}
	if os.Geteuid() == 0 {
		t.Fatal("initdb fixture must run as an unprivileged user")
	}
	for _, name := range []string{"initdb", "pg_ctl", "psql"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.MkdirTemp("", "lmm-source-type-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(root, "socket")
	if err := os.Mkdir(socket, 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "LANG=C", "LC_ALL=C", "PGHOST=" + socket, "PGUSER=lmm_source_type_test", "PGDATABASE=postgres", "PGCONNECT_TIMEOUT=5"}
	run := func(input string, name string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.Env = env
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v: %s", name, err, output)
		}
		return output
	}
	sql := func(query string) []byte {
		t.Helper()
		return run(query, "psql", "-X", "-A", "-t", "-q", "-v", "ON_ERROR_STOP=1")
	}
	run("", "initdb", "-D", data, "--auth=trust", "--no-locale", "--encoding=UTF8", "--username=lmm_source_type_test")
	run("", "pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", "-h '' -k "+socket, "start")
	t.Cleanup(func() { run("", "pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	if strings.TrimSpace(string(sql("SHOW listen_addresses"))) != "" {
		t.Fatal("private fixture must have no TCP listeners")
	}
	version := strings.TrimSpace(string(sql("SHOW server_version")))
	sql("CREATE SCHEMA app; CREATE TABLE app.example(id bigint PRIMARY KEY, label text); INSERT INTO app.example VALUES(1,'fixture'); CREATE SCHEMA external_fixture;")
	query, err := schemaMaintenanceBackupSourceQuery("app")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(reject bool) {
		t.Helper()
		raw := sql(query)
		var s productionSchemaMaintenanceBackupSource
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		_, err := decodeSchemaMaintenanceBackupSource(raw)
		if reject {
			if s.UnsupportedObjects == 0 || err == nil {
				t.Fatal("custom type source was accepted")
			}
		} else if s.UnsupportedObjects != 0 || err != nil {
			t.Fatalf("built-in source rejected: unsupported=%d error=%v", s.UnsupportedObjects, err)
		}
	}
	snapshot(false)
	for _, fixture := range []struct{ name, create, column string }{
		{"external_enum", "CREATE TYPE external_fixture.test_type AS ENUM('one')", "external_fixture.test_type"},
		{"external_domain", "CREATE DOMAIN external_fixture.test_type AS bigint CHECK(VALUE>0)", "external_fixture.test_type"},
		{"external_composite_array", "CREATE TYPE external_fixture.test_type AS (value bigint)", "external_fixture.test_type[]"},
		{"application_enum", "CREATE TYPE app.test_type AS ENUM('one')", "app.test_type"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			sql(fixture.create + "; ALTER TABLE app.example ADD COLUMN unsupported " + fixture.column)
			snapshot(true)
			sql("ALTER TABLE app.example DROP COLUMN unsupported; DROP TYPE " + strings.TrimSuffix(fixture.column, "[]"))
			snapshot(false)
		})
	}
	t.Logf("postgres=%s no_tcp=true builtins_accepted=true external_enum_domain_composite_array_rejected=true app_enum_still_rejected=true", version)
}
