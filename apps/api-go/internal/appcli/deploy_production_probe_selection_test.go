package appcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type probeSelectionRunner struct {
	schema  string
	output  string
	failure bool
	queries []productionCommand
}

func (runner *probeSelectionRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	runner.queries = append(runner.queries, command)
	if !command.Sensitive || command.Name != commandPSQL {
		return nil, errors.New("probe selection must use sensitive read-only SQL")
	}
	if strings.Contains(strings.Join(command.Args, " "), "current_schema") {
		return []byte(runner.schema + "\n"), nil
	}
	if runner.failure {
		return nil, errors.New("injected query failure")
	}
	return []byte(runner.output), nil
}

func TestProductionProbeSelectionCredentialBoundary(t *testing.T) {
	const key = "manual_test_01234567890123456789"
	for _, tc := range []struct {
		name, schema, result                       string
		queryFailure, existing, symlink, wantError bool
	}{
		{name: "manual bearer", schema: "public", result: key + "\n"},
		{name: "OAuth only or no eligible credential", schema: "public", wantError: true},
		{name: "malformed stored key", schema: "public", result: "private key\n", wantError: true},
		{name: "multiple rows violate limit", schema: "public", result: key + "\n" + key + "\n", wantError: true},
		{name: "private query failure", schema: "public", queryFailure: true, wantError: true},
		{name: "unsafe schema", schema: "pg_catalog", wantError: true},
		{name: "existing private file", schema: "public", result: key, existing: true, wantError: true},
		{name: "existing symlink", schema: "public", result: key, symlink: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tokenPath := filepath.Join(root, "probe-token")
			preserved := []byte("do-not-replace")
			if tc.existing {
				if err := os.WriteFile(tokenPath, preserved, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.symlink {
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, preserved, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, tokenPath); err != nil {
					t.Fatal(err)
				}
			}
			runner := &probeSelectionRunner{schema: tc.schema, output: tc.result, failure: tc.queryFailure}
			runtime := &productionRuntime{runner: runner}
			schema, err := runtime.captureDatabaseAccess(context.Background(), productionWorkspace{probeToken: tokenPath}, []byte("SQL_DSN=postgres://fixture:fixture@127.0.0.1:5432/fixture?sslmode=disable\n"))
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected selection result: schema=%q err=%v", schema, err)
			}
			if err != nil && (strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "private key")) {
				t.Fatal("credential leaked in selection error")
			}
			if tc.schema == "public" {
				if len(runner.queries) != 2 {
					t.Fatalf("wanted schema and selection queries, got %d", len(runner.queries))
				}
				query := strings.Join(runner.queries[1].Args, " ")
				for _, required := range []string{"COALESCE((to_jsonb(tokens)->>'oauth_managed')::boolean, false) = false", "tokens.deleted_at IS NULL", "tokens.status = 1", "users.status = 1", "users.role >= 10", "tokens.expired_time", "tokens.remain_quota > 0", "BTRIM(tokens.allow_ips)", "LIMIT 1"} {
					if !strings.Contains(query, required) {
						t.Errorf("missing credential boundary %q", required)
					}
				}
			}
			if tc.existing || tc.symlink {
				actual, readErr := os.ReadFile(tokenPath)
				if readErr != nil || string(actual) != string(preserved) {
					t.Fatal("existing private input was changed")
				}
			} else if tc.wantError {
				if _, statErr := os.Lstat(tokenPath); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("failed selection left a credential file")
				}
			} else {
				actual, readErr := os.ReadFile(tokenPath)
				if readErr != nil || string(actual) != "sk-"+key+"\n" {
					t.Fatal("selected credential was not written exactly")
				}
				info, statErr := os.Lstat(tokenPath)
				if statErr != nil || info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
					t.Fatal("probe credential is not a private regular file")
				}
			}
		})
	}
}
