//go:build linux

package appcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type incident343EmptyDumpFailureRunner struct{ *incident343Runner }

func (r *incident343EmptyDumpFailureRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandPGDump && r.backupFail {
		for _, arg := range command.Args {
			if strings.HasPrefix(arg, "--file=") {
				if err := os.WriteFile(strings.TrimPrefix(arg, "--file="), nil, 0600); err != nil {
					return nil, err
				}
			}
		}
	}
	return r.incident343Runner.Run(ctx, command)
}

func TestIncident343NativeRetryNeedsFreshBackupBeforeSchema(t *testing.T) {
	f, manifest, runner := incident343Fixture(t)
	f.runtime.runner = &incident343EmptyDumpFailureRunner{runner}
	runner.backupFail = true
	_, err := f.runtime.recoverInstalledSchema(context.Background(), f.workspace, manifest, func(context.Context, string, string) error {
		t.Fatal("schema invoked after failed backup")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "backup failed") {
		t.Fatalf("unexpected initial result: %v", err)
	}
	root := filepath.Join(f.workspace.stateDir, "incident-343-schema-recovery")
	runner.backupFail = false
	calls := 0
	status, err := f.runtime.recoverInstalledSchema(context.Background(), f.workspace, manifest, func(context.Context, string, string) error {
		calls++
		dump := filepath.Join(root, "backup-retry-1", "before-schema.database.dump")
		info, statErr := os.Stat(dump)
		if statErr != nil || info.Size() == 0 {
			t.Fatal("schema invoked without new complete backup", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, "backup-retry-1", "database.sha256")); statErr != nil {
			t.Fatal("schema invoked without verified backup digest", statErr)
		}
		return nil
	})
	if err != nil || status.Phase != "AWAITING_CONFIRMATION" || calls != 1 {
		t.Fatalf("retry result=%+v schema_calls=%d error=%v", status, calls, err)
	}
	info, err := os.Stat(filepath.Join(root, "before-schema.database.dump"))
	if err != nil || info.Size() != 0 {
		t.Fatal("original failed dump was replaced", err)
	}
}
