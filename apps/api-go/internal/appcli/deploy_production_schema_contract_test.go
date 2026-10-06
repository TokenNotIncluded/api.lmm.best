//go:build !windows

package appcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type existingSchemaTestRunner struct {
	run func(productionCommand) ([]byte, error)
}

func (runner existingSchemaTestRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	return runner.run(command)
}

func testExistingSchemaSnapshot() []byte {
	metadata := map[string]any{}
	for _, section := range existingSchemaMetadataSections {
		metadata[section] = []any{}
	}
	metadata["namespace"] = map[string]any{"oid": 2200, "name": "public", "owner": 10, "acl": "{postgres=UC/postgres}"}
	metadata["server_version"] = "180000"
	metadata["columns"] = []any{map[string]any{"relation": 17000, "name": "quota", "type": 20, "not_null": true}}
	result, _ := json.Marshal(map[string]any{"system_identifier": "7560021234567890123", "database": "lmm", "database_oid": 16384,
		"schema": "public", "schema_oid": 2200, "transaction_read_only": "on", "metadata": metadata})
	return result
}

func testExistingSchemaContract(t *testing.T) *productionExistingSchemaContract {
	t.Helper()
	contract, err := decodeExistingSchemaSnapshot(testExistingSchemaSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	return &contract
}

func testExistingSchemaIdentitySnapshot() []byte {
	var snapshot map[string]any
	_ = json.Unmarshal(testExistingSchemaSnapshot(), &snapshot)
	delete(snapshot, "metadata")
	result, _ := json.Marshal(snapshot)
	return result
}

func TestExistingSchemaContractPrivateSeal(t *testing.T) {
	contract := testExistingSchemaContract(t)
	canonical, _ := json.MarshalIndent(contract, "", "  ")
	canonical = append(canonical, '\n')
	write := func(t *testing.T, data []byte) (string, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "schema.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path, fmt.Sprintf("%x", sha256.Sum256(data))
	}
	t.Run("valid", func(t *testing.T) {
		path, digest := write(t, canonical)
		got, err := loadProductionExistingSchemaContract(path, digest)
		if err != nil || *got != *contract {
			t.Fatalf("sealed contract rejected: %v", err)
		}
	})
	for _, kind := range []string{"digest", "tampered", "symlink", "hardlink", "permissions", "noncanonical", "unknown-field", "trailing-json"} {
		t.Run(kind, func(t *testing.T) {
			path, digest := write(t, canonical)
			switch kind {
			case "digest":
				digest = strings.Repeat("0", 64)
			case "tampered":
				_ = os.WriteFile(path, bytes.Replace(canonical, []byte(`"public"`), []byte(`"private"`), 1), 0600)
			case "symlink":
				target := path + ".original"
				_ = os.Rename(path, target)
				_ = os.Symlink(target, path)
			case "hardlink":
				if err := os.Link(path, path+".other"); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				_ = os.Chmod(path, 0644)
			case "noncanonical":
				compact, _ := json.Marshal(contract)
				path, digest = write(t, compact)
			case "unknown-field":
				altered := bytes.Replace(canonical, []byte("{\n"), []byte("{\n  \"skip_apply\": true,\n"), 1)
				path, digest = write(t, altered)
			case "trailing-json":
				path, digest = write(t, append(append([]byte{}, canonical...), []byte("{}\n")...))
			}
			if _, err := loadProductionExistingSchemaContract(path, digest); err == nil {
				t.Fatal("unsafe schema seal accepted")
			}
		})
	}
}

func TestExistingSchemaSnapshotRejectsDriftAndIncompleteEvidence(t *testing.T) {
	expected := testExistingSchemaContract(t)
	var snapshot map[string]any
	if err := json.Unmarshal(testExistingSchemaSnapshot(), &snapshot); err != nil {
		t.Fatal(err)
	}
	// Whitespace and JSON key order are not schema drift.
	formatted, _ := json.MarshalIndent(snapshot, "", " ")
	actual, err := decodeExistingSchemaSnapshot(formatted)
	if err != nil || actual != *expected {
		t.Fatalf("equivalent snapshot differs: %v", err)
	}
	for _, kind := range []string{"cluster", "database", "oid", "schema-oid", "column", "sequence-owner", "owner-acl", "read-write", "missing", "null", "wrong-shape", "extra"} {
		t.Run(kind, func(t *testing.T) {
			var changed map[string]any
			_ = json.Unmarshal(testExistingSchemaSnapshot(), &changed)
			metadata := changed["metadata"].(map[string]any)
			switch kind {
			case "cluster":
				changed["system_identifier"] = "7560021234567890999"
			case "database":
				changed["database"] = "another"
			case "oid":
				changed["database_oid"] = 999
			case "schema-oid":
				changed["schema_oid"] = 999
			case "column":
				metadata["columns"] = []any{map[string]any{"name": "quota", "type": 1700}}
			case "sequence-owner":
				metadata["dependencies"] = []any{map[string]any{"objid": 17001, "refobjid": 17000, "deptype": "a"}}
			case "owner-acl":
				metadata["namespace"].(map[string]any)["acl"] = "{public=UC/postgres}"
			case "read-write":
				changed["transaction_read_only"] = "off"
			case "missing":
				delete(metadata, "policies")
			case "null":
				metadata["namespace"] = nil
			case "wrong-shape":
				metadata["sequences"] = "omitted"
			case "extra":
				metadata["unsealed"] = []any{}
			}
			data, _ := json.Marshal(changed)
			contract, err := decodeExistingSchemaSnapshot(data)
			if err == nil && contract == *expected {
				t.Fatal("changed or incomplete schema accepted as equivalent")
			}
		})
	}
}

func TestExistingSchemaCaptureUsesOnlyReadOnlyCatalogsWithoutPasswordArgv(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "lmm-api-go.env"), []byte("SQL_DSN=postgres://user:fixture-password@127.0.0.1/lmm?sslmode=require\n"), 0600); err != nil {
		t.Fatal(err)
	}
	queries := 0
	runtime := productionRuntime{paths: productionPaths{ConfigDir: directory}, runner: existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		queries++
		if command.Name != commandPSQL || !command.Sensitive || command.OutputLimit == 0 || command.Timeout == 0 {
			t.Fatal("schema inspection is not a bounded private PostgreSQL command")
		}
		if strings.Contains(strings.Join(command.Args, " "), "fixture-password") {
			t.Fatal("database password leaked into argv")
		}
		query := command.Args[slices.Index(command.Args, "--command")+1]
		if !strings.HasPrefix(query, "BEGIN READ ONLY;\n") || !strings.HasSuffix(query, "ROLLBACK;") || !strings.Contains(query, "SET LOCAL search_path TO pg_catalog;") {
			t.Fatal("catalog inspection is not fenced read-only")
		}
		for _, forbidden := range []string{"nextval(", "setval(", "last_value", "log_cnt", "is_called", ".users", ".casbin_rule", "INSERT ", "DELETE ", "UPDATE "} {
			if strings.Contains(query, forbidden) {
				t.Fatalf("schema query accesses mutable application data: %s", forbidden)
			}
		}
		return testExistingSchemaSnapshot(), nil
	}}}
	if err := runtime.verifyExistingSchemaContract(context.Background(), testExistingSchemaContract(t)); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("catalog queries=%d", queries)
	}
	wrong := *testExistingSchemaContract(t)
	wrong.MetadataSHA256 = strings.Repeat("a", 64)
	if err := runtime.verifyExistingSchemaContract(context.Background(), &wrong); err == nil {
		t.Fatal("schema drift accepted")
	}
}

func TestExistingSchemaMigrationFencesURIOverridesAndFinancialReplay(t *testing.T) {
	t.Setenv("LMM_CREDIT_TRANSITION_PLAN", "inherited-plan")
	t.Setenv("LMM_CREDIT_TRANSITION_SHA256", strings.Repeat("f", 64))
	t.Setenv("PGOPTIONS", "-c default_transaction_read_only=off -c search_path=other")
	t.Setenv("DATABASE_URL", "postgres://unexpected/inherited")
	t.Setenv("LOG_SQL_DSN", "postgres://unexpected/log-inherited")
	directory := t.TempDir()
	environment := []byte("SQL_DSN=postgres://user:fixture-password@127.0.0.1/lmm?sslmode=require&options=-c%20default_transaction_read_only%3Doff&search_path=other\nLMM_CREDIT_TRANSITION_PLAN=configured-plan\nLMM_CREDIT_TRANSITION_SHA256=" + strings.Repeat("a", 64) + "\n")
	if err := os.WriteFile(filepath.Join(directory, "lmm-api-go.env"), environment, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runtime := productionRuntime{paths: productionPaths{ConfigDir: directory}, runner: existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		calls++
		if !slices.Contains(command.Args, "--verify") || slices.Contains(command.Args, "--apply") || !command.Sensitive {
			t.Fatal("verification executed with unsafe mode")
		}
		values := map[string]string{}
		for _, entry := range command.Env {
			key, value, _ := strings.Cut(entry, "=")
			values[key] = value
		}
		for _, key := range existingSchemaFinancialEnvironment {
			if _, present := values[key]; present {
				t.Fatal("financial replay environment reached verification child")
			}
		}
		if _, present := values["DATABASE_URL"]; present {
			t.Fatal("inherited secondary database reached verification child")
		}
		if _, present := values["LOG_SQL_DSN"]; present {
			t.Fatal("inherited independent log database reached verification child")
		}
		parsed, err := url.Parse(values["SQL_DSN"])
		if err != nil || parsed.Query().Get("default_transaction_read_only") != "on" || parsed.Query().Get("search_path") != "public" || parsed.Query().Get("sslmode") != "require" ||
			parsed.Query().Get("options") != "-c search_path=public -c default_transaction_read_only=on" || values["PGOPTIONS"] != parsed.Query().Get("options") || values["LMM_DB_MIGRATION_MODE"] != "verify" {
			t.Fatal("DSN or inherited environment bypasses read-only fence")
		}
		return nil, nil
	}}}
	workspace := productionWorkspace{root: t.TempDir()}
	manifest := productionManifest{SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: testExistingSchemaContract(t), DatabaseSchema: "public"}
	if err := runtime.runMigration(context.Background(), workspace, manifest, migrationRun{name: "candidate-verify", binary: "/fixture/provider", mode: "verify"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("actual candidate verification did not execute")
	}
	if err := runtime.runMigration(context.Background(), workspace, manifest, migrationRun{name: "candidate-apply", binary: "/fixture/provider", mode: "apply"}); err == nil || calls != 1 {
		t.Fatal("verify-existing executed apply")
	}
	manifest.MaintenanceHandoff = &productionMaintenanceHandoff{}
	if err := runtime.runMigration(context.Background(), workspace, manifest, migrationRun{name: "candidate-verify", binary: "/fixture/provider", mode: "verify"}); err == nil || calls != 1 {
		t.Fatal("verify-existing accepted financial maintenance")
	}
}

func TestExistingSchemaLogDatabaseIsBoundAndReadOnly(t *testing.T) {
	runtime := productionRuntime{}
	dsn := "postgres://user:fixture-password@127.0.0.1/lmm?sslmode=require"
	environment, err := runtime.existingSchemaMigrationEnvironment([]byte("SQL_DSN="+dsn+"\nLOG_SQL_DSN="+dsn+"\n"), "public")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, assignment := range environment {
		key, value, _ := strings.Cut(assignment, "=")
		values[key] = value
	}
	if values["LOG_SQL_DSN"] != values["SQL_DSN"] || !strings.Contains(values["LOG_SQL_DSN"], "default_transaction_read_only=on") {
		t.Fatal("same-database log connection escaped read-only fence")
	}
	if _, err := runtime.existingSchemaMigrationEnvironment([]byte("SQL_DSN="+dsn+"\nLOG_SQL_DSN=postgres://user@another/logs\n"), "public"); err == nil {
		t.Fatal("unsealed independent log database accepted")
	}
}

func TestExistingSchemaStartupChecksLoadedAndActualGeneration(t *testing.T) {
	for _, failure := range []string{"", "loaded-apply", "config-apply", "config-log-database", "loaded-financial", "process-financial", "process-apply", "process-database", "process-log-database", "process-schema", "process-cluster", "loaded-schema", "loaded-cluster", "generation", "extra-envfile", "unset", "exec-apply", "start-hook", "stop-hook", "inactive"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			configuration := "SQL_DSN=postgres://user@127.0.0.1/lmm\n"
			if failure == "config-apply" {
				configuration += "LMM_DB_MIGRATION_MODE=apply\n"
			}
			if failure == "config-log-database" {
				configuration += "LOG_SQL_DSN=postgres://another/logs\n"
			}
			if err := os.WriteFile(filepath.Join(directory, "lmm-api-go.env"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			runtime := productionRuntime{paths: productionPaths{ConfigDir: directory, Service: "lmm-api.service", InstalledBinary: "/fixture/lmm-api"}}
			runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
				if command.Name == commandPSQL {
					if !command.Sensitive || !strings.Contains(strings.Join(command.Args, " "), "BEGIN READ ONLY") {
						t.Fatal("effective schema probe is not private/read-only")
					}
					if slices.Contains(command.Env, "PGOPTIONS=-c search_path=other") {
						return bytes.Replace(testExistingSchemaIdentitySnapshot(), []byte(`"schema":"public"`), []byte(`"schema":"other"`), 1), nil
					}
					if slices.Contains(command.Env, "PGHOST=another-cluster") {
						return bytes.Replace(testExistingSchemaIdentitySnapshot(), []byte(`7560021234567890123`), []byte(`7560021234567890999`), 1), nil
					}
					if !slices.Contains(command.Env, "PGOPTIONS=") {
						t.Fatal("ambient PGOPTIONS leaked into actual process schema proof")
					}
					return testExistingSchemaIdentitySnapshot(), nil
				}
				calls++
				if command.Name != commandSystemctl || !command.Sensitive {
					return nil, errors.New("unexpected startup proof command")
				}
				mode := "verify"
				if failure == "loaded-apply" {
					mode = "apply"
				}
				loadedEnv := "GIN_MODE=release LMM_DB_MIGRATION_MODE=" + mode
				if failure == "loaded-financial" {
					loadedEnv += " LMM_CREDIT_TRANSITION_PLAN=old-plan"
				}
				if failure == "loaded-schema" {
					loadedEnv += " \"PGOPTIONS=-c search_path=other\""
				}
				if failure == "loaded-cluster" {
					loadedEnv += " PGHOST=another-cluster"
				}
				files := filepath.Join(directory, "lmm-api-go.env") + " (ignore_errors=yes)"
				if failure == "extra-envfile" {
					files += " /fixture/extra.env (ignore_errors=no)"
				}
				unset := ""
				if failure == "unset" {
					unset = "LMM_DB_MIGRATION_MODE"
				}
				pid, state := "1234", "active"
				if failure == "inactive" {
					pid, state = "0", "inactive"
				}
				if failure == "loaded-schema" || failure == "loaded-cluster" {
					pid, state = "0", "inactive"
				}
				invocation := strings.Repeat("1", 32)
				if failure == "generation" && calls > 1 {
					invocation = strings.Repeat("2", 32)
				}
				start := "{ path=/fixture/lmm-api ; argv[]=/fixture/lmm-api serve ; ignore_errors=no ; }"
				if failure == "exec-apply" {
					start = "{ path=/fixture/lmm-api ; argv[]=/fixture/lmm-api migrate --apply ; ignore_errors=no ; }"
				}
				startHook, stopHook := "", ""
				if failure == "start-hook" {
					startHook = "{ path=/fixture/write-db ; }"
				}
				if failure == "stop-hook" {
					stopHook = "{ path=/fixture/write-db ; }"
				}
				return []byte(fmt.Sprintf("Environment=%s\nEnvironmentFiles=%s\nPassEnvironment=\nUnsetEnvironment=%s\nMainPID=%s\nInvocationID=%s\nActiveState=%s\nExecStart=%s\nExecStartPre=%s\nExecStartPost=\nExecCondition=\nExecStop=%s\nExecStopPost=\n", loadedEnv, files, unset, pid, invocation, state, start, startHook, stopHook)), nil
			}}
			runtime.maintenanceProcessEnvironment = func(pid int) ([]byte, error) {
				mode, dsn := "verify", "postgres://user@127.0.0.1/lmm"
				if failure == "process-apply" {
					mode = "apply"
				}
				if failure == "process-database" {
					dsn = "postgres://user@127.0.0.1/another"
				}
				value := "LMM_DB_MIGRATION_MODE=" + mode + "\x00SQL_DSN=" + dsn + "\x00"
				if failure == "process-financial" {
					value += "LMM_CREDIT_TRANSITION_SHA256=old-digest\x00"
				}
				if failure == "process-schema" {
					value += "PGOPTIONS=-c search_path=other\x00"
				}
				if failure == "process-cluster" {
					value += "PGHOST=another-cluster\x00"
				}
				if failure == "process-log-database" {
					value += "LOG_SQL_DSN=postgres://another/logs\x00"
				}
				return []byte(value), nil
			}
			err := runtime.verifyExistingSchemaStartupMode(context.Background(), productionManifest{SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: testExistingSchemaContract(t)})
			if failure == "" || failure == "inactive" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe service startup accepted")
			}
		})
	}
}
